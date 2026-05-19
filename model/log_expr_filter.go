package model

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/conf"
	"github.com/expr-lang/expr/parser"
	"gorm.io/gorm"
)

const maxLogExprLength = 4096
const maxLogExprInItems = 100
const maxLogExprStringLength = 1024

type logExprFieldKind int

const (
	logExprFieldString logExprFieldKind = iota
	logExprFieldInt
	logExprFieldBool
	logExprFieldDate
)

type logExprField struct {
	Column    string
	Kind      logExprFieldKind
	Timestamp bool
}

type logExprCompiler struct {
	fields    map[string]logExprField
	variables map[string]logExprSQL
}

// newLogExprCompiler builds the allow-list for call-log expr search. Only
// identifiers listed here can be translated into SQL, keeping expr parsing
// AST-only and avoiding runtime evaluation of user input.
func newLogExprCompiler(includeAdminFields bool, variables map[string]logExprSQL) logExprCompiler {
	fields := map[string]logExprField{
		"id":                  {Column: "logs.id", Kind: logExprFieldInt},
		"user_id":             {Column: "logs.user_id", Kind: logExprFieldInt},
		"created_at":          {Column: "logs.created_at", Kind: logExprFieldInt, Timestamp: true},
		"createdAt":           {Column: "logs.created_at", Kind: logExprFieldInt, Timestamp: true},
		"timestamp":           {Column: "logs.created_at", Kind: logExprFieldInt, Timestamp: true},
		"type":                {Column: "logs.type", Kind: logExprFieldInt},
		"log_type":            {Column: "logs.type", Kind: logExprFieldInt},
		"content":             {Column: "logs.content", Kind: logExprFieldString},
		"token_name":          {Column: "logs.token_name", Kind: logExprFieldString},
		"token":               {Column: "logs.token_name", Kind: logExprFieldString},
		"model_name":          {Column: "logs.model_name", Kind: logExprFieldString},
		"model":               {Column: "logs.model_name", Kind: logExprFieldString},
		"quota":               {Column: "logs.quota", Kind: logExprFieldInt},
		"prompt_tokens":       {Column: "logs.prompt_tokens", Kind: logExprFieldInt},
		"completion_tokens":   {Column: "logs.completion_tokens", Kind: logExprFieldInt},
		"use_time":            {Column: "logs.use_time", Kind: logExprFieldInt},
		"is_stream":           {Column: "logs.is_stream", Kind: logExprFieldBool},
		"stream":              {Column: "logs.is_stream", Kind: logExprFieldBool},
		"token_id":            {Column: "logs.token_id", Kind: logExprFieldInt},
		"group":               {Column: "logs." + logGroupCol, Kind: logExprFieldString},
		"ip":                  {Column: "logs.ip", Kind: logExprFieldString},
		"request_id":          {Column: "logs.request_id", Kind: logExprFieldString},
		"requestId":           {Column: "logs.request_id", Kind: logExprFieldString},
		"upstream_request_id": {Column: "logs.upstream_request_id", Kind: logExprFieldString},
		"upstreamRequestId":   {Column: "logs.upstream_request_id", Kind: logExprFieldString},
		"other":               {Column: "logs.other", Kind: logExprFieldString},
	}
	if includeAdminFields {
		fields["username"] = logExprField{Column: "logs.username", Kind: logExprFieldString}
		fields["channel"] = logExprField{Column: "logs.channel_id", Kind: logExprFieldInt}
		fields["channel_id"] = logExprField{Column: "logs.channel_id", Kind: logExprFieldInt}
		fields["channel_name"] = logExprField{Column: "channels.name", Kind: logExprFieldString}
		fields["channelName"] = logExprField{Column: "channels.name", Kind: logExprFieldString}
	}
	return logExprCompiler{fields: fields, variables: variables}
}

func applyLogExprFilter(tx *gorm.DB, exprStr string, includeAdminFields bool) (*gorm.DB, error) {
	exprStr = strings.TrimSpace(exprStr)
	if exprStr == "" {
		return tx, nil
	}
	where, args, needsChannelJoin, err := buildLogExprSQL(exprStr, includeAdminFields)
	if err != nil {
		return nil, err
	}
	if needsChannelJoin {
		if !logExprCanJoinChannels() {
			return nil, errors.New("channel_name 表达式筛选需要连接渠道表，独立日志数据库模式下不支持，请改用 channel 或 channel_id 筛选")
		}
		tx = tx.Joins("LEFT JOIN channels ON channels.id = logs.channel_id")
	}
	return tx.Where(where, args...), nil
}

func buildLogExprSQL(exprStr string, includeAdminFields bool) (where string, args []any, needsChannelJoin bool, err error) {
	return buildLogExprSQLAt(exprStr, includeAdminFields, time.Now())
}

func buildLogExprSQLAt(exprStr string, includeAdminFields bool, now time.Time) (where string, args []any, needsChannelJoin bool, err error) {
	if len(exprStr) > maxLogExprLength {
		return "", nil, false, fmt.Errorf("表达式过长，最多允许 %d 个字符", maxLogExprLength)
	}

	cfg := conf.CreateNew()
	cfg.MaxNodes = 256
	tree, err := parser.ParseWithConfig(exprStr, cfg)
	if err != nil {
		return "", nil, false, fmt.Errorf("表达式解析失败: %w", err)
	}
	if tree == nil || tree.Node == nil {
		return "", nil, false, errors.New("表达式不能为空")
	}

	variables := buildLogExprVariables(tree.Node, now)
	compiler := newLogExprCompiler(includeAdminFields, variables)
	compiled, err := compiler.compileBool(tree.Node)
	if err != nil {
		return "", nil, false, err
	}
	return compiled.sql, compiled.args, compiled.needsChannelJoin, nil
}

func (c logExprCompiler) compileBool(node ast.Node) (logExprSQL, error) {
	switch n := node.(type) {
	case *ast.BinaryNode:
		op := normalizeLogExprOperator(n.Operator)
		switch op {
		case "&&", "and", "||", "or":
			left, err := c.compileBool(n.Left)
			if err != nil {
				return logExprSQL{}, err
			}
			right, err := c.compileBool(n.Right)
			if err != nil {
				return logExprSQL{}, err
			}
			joiner := "AND"
			if op == "||" || op == "or" {
				joiner = "OR"
			}
			return combineLogExprSQL(joiner, left, right), nil
		default:
			return c.compileComparison(n)
		}
	case *ast.UnaryNode:
		op := normalizeLogExprOperator(n.Operator)
		if op != "!" && op != "not" {
			return logExprSQL{}, fmt.Errorf("不支持的一元操作符 %q", n.Operator)
		}
		inner, err := c.compileBool(n.Node)
		if err != nil {
			return logExprSQL{}, err
		}
		inner.sql = fmt.Sprintf("NOT (%s)", inner.sql)
		return inner, nil
	case *ast.IdentifierNode:
		if variable, ok := c.variables[n.Value]; ok {
			return cloneLogExprSQL(variable), nil
		}
		field, ok := c.fields[n.Value]
		if !ok {
			return logExprSQL{}, fmt.Errorf("不支持的日志字段 %q", n.Value)
		}
		if field.Kind != logExprFieldBool {
			return logExprSQL{}, fmt.Errorf("字段 %q 需要与值比较", n.Value)
		}
		return logExprSQL{
			sql:              fmt.Sprintf("%s = ?", field.Column),
			args:             []any{true},
			needsChannelJoin: logExprFieldNeedsChannelJoin(field),
		}, nil
	case *ast.BoolNode:
		if n.Value {
			return logExprSQL{sql: "1 = 1"}, nil
		}
		return logExprSQL{sql: "1 = 0"}, nil
	default:
		return logExprSQL{}, fmt.Errorf("表达式必须是布尔条件，当前节点 %T 不支持", node)
	}
}

func (c logExprCompiler) compileComparison(node *ast.BinaryNode) (logExprSQL, error) {
	op := normalizeLogExprOperator(node.Operator)
	if op == "in" {
		return c.compileIn(node.Left, node.Right, false)
	}
	if op == "not in" {
		return c.compileIn(node.Left, node.Right, true)
	}

	field, literal, swapped, err := c.extractFieldLiteral(node.Left, node.Right)
	if err != nil {
		return logExprSQL{}, err
	}

	if swapped {
		op = reverseLogExprComparisonOperator(op)
	}

	switch op {
	case "==", "!=", ">", ">=", "<", "<=":
		return c.compileRelational(field, op, literal)
	case "contains", "startsWith", "endsWith", "matches":
		return c.compileStringMatch(field, op, literal, false)
	default:
		return logExprSQL{}, fmt.Errorf("不支持的比较操作符 %q", node.Operator)
	}
}

func (c logExprCompiler) compileRelational(field logExprResolvedField, op string, literal logExprLiteral) (logExprSQL, error) {
	if literal.isNil {
		if op != "==" && op != "!=" {
			return logExprSQL{}, errors.New("nil 只能用于 == 或 != 比较")
		}
		isNull := "IS NULL"
		if op == "!=" {
			isNull = "IS NOT NULL"
		}
		return logExprSQL{sql: fmt.Sprintf("%s %s", field.Column, isNull), needsChannelJoin: field.needsChannelJoin}, nil
	}

	value, err := coerceLogExprLiteral(field, literal)
	if err != nil {
		return logExprSQL{}, err
	}

	sqlOp := map[string]string{
		"==": "=",
		"!=": "<>",
		">":  ">",
		">=": ">=",
		"<":  "<",
		"<=": "<=",
	}[op]

	return logExprSQL{
		sql:              fmt.Sprintf("%s %s ?", field.Column, sqlOp),
		args:             []any{value},
		needsChannelJoin: field.needsChannelJoin,
	}, nil
}

func (c logExprCompiler) compileStringMatch(field logExprResolvedField, op string, literal logExprLiteral, negate bool) (logExprSQL, error) {
	if field.Kind != logExprFieldString {
		return logExprSQL{}, fmt.Errorf("操作符 %q 只能用于字符串字段", op)
	}
	if literal.isNil || literal.kind != logExprFieldString {
		return logExprSQL{}, fmt.Errorf("操作符 %q 需要字符串值", op)
	}
	if op == "matches" {
		return logExprSQL{}, errors.New("matches 正则匹配无法跨数据库转换为 SQL，请使用 contains/startsWith/endsWith")
	}

	pattern := escapeLogExprLikePattern(literal.value.(string))
	switch op {
	case "contains":
		pattern = "%" + pattern + "%"
	case "startsWith":
		pattern += "%"
	case "endsWith":
		pattern = "%" + pattern
	}

	operator := "LIKE"
	if negate {
		operator = "NOT LIKE"
	}
	return logExprSQL{
		sql:              fmt.Sprintf("%s %s ? ESCAPE '!'", field.Column, operator),
		args:             []any{pattern},
		needsChannelJoin: field.needsChannelJoin,
	}, nil
}

func (c logExprCompiler) compileIn(left ast.Node, right ast.Node, negate bool) (logExprSQL, error) {
	field, err := c.resolveField(left)
	if err != nil {
		return logExprSQL{}, err
	}
	arrayNode, ok := right.(*ast.ArrayNode)
	if !ok {
		return logExprSQL{}, errors.New("in 操作符右侧必须是字面量数组")
	}
	if len(arrayNode.Nodes) == 0 {
		if negate {
			return logExprSQL{sql: "1 = 1", needsChannelJoin: field.needsChannelJoin}, nil
		}
		return logExprSQL{sql: "1 = 0", needsChannelJoin: field.needsChannelJoin}, nil
	}
	if len(arrayNode.Nodes) > maxLogExprInItems {
		return logExprSQL{}, fmt.Errorf("in 数组最多允许 %d 个元素", maxLogExprInItems)
	}

	values := make([]any, 0, len(arrayNode.Nodes))
	for _, item := range arrayNode.Nodes {
		literal, err := literalFromLogExprNode(item)
		if err != nil {
			return logExprSQL{}, err
		}
		if literal.isNil {
			return logExprSQL{}, errors.New("in 数组中不支持 nil")
		}
		value, err := coerceLogExprLiteral(field, literal)
		if err != nil {
			return logExprSQL{}, err
		}
		values = append(values, value)
	}

	operator := "IN"
	if negate {
		operator = "NOT IN"
	}
	return logExprSQL{
		sql:              fmt.Sprintf("%s %s ?", field.Column, operator),
		args:             []any{values},
		needsChannelJoin: field.needsChannelJoin,
	}, nil
}

func (c logExprCompiler) extractFieldLiteral(left ast.Node, right ast.Node) (field logExprResolvedField, literal logExprLiteral, swapped bool, err error) {
	leftField, leftErr := c.resolveField(left)
	if leftErr == nil {
		lit, litErr := literalFromLogExprNode(right)
		if litErr != nil {
			return logExprResolvedField{}, logExprLiteral{}, false, litErr
		}
		return leftField, lit, false, nil
	}
	rightField, rightErr := c.resolveField(right)
	if rightErr == nil {
		lit, litErr := literalFromLogExprNode(left)
		if litErr != nil {
			return logExprResolvedField{}, logExprLiteral{}, false, litErr
		}
		return rightField, lit, true, nil
	}
	if _, ok := left.(*ast.IdentifierNode); ok {
		return logExprResolvedField{}, logExprLiteral{}, false, leftErr
	}
	if _, ok := right.(*ast.IdentifierNode); ok {
		return logExprResolvedField{}, logExprLiteral{}, false, rightErr
	}
	return logExprResolvedField{}, logExprLiteral{}, false, errors.New("比较表达式必须包含一个日志字段和一个字面量")
}

func (c logExprCompiler) resolveField(node ast.Node) (logExprResolvedField, error) {
	identifier, ok := node.(*ast.IdentifierNode)
	if !ok {
		return logExprResolvedField{}, errors.New("左侧必须是日志字段")
	}
	field, ok := c.fields[identifier.Value]
	if !ok {
		return logExprResolvedField{}, fmt.Errorf("不支持的日志字段 %q", identifier.Value)
	}
	return logExprResolvedField{
		Name:             identifier.Value,
		Column:           field.Column,
		Kind:             field.Kind,
		Timestamp:        field.Timestamp,
		needsChannelJoin: logExprFieldNeedsChannelJoin(field),
	}, nil
}

func literalFromLogExprNode(node ast.Node) (logExprLiteral, error) {
	switch n := node.(type) {
	case *ast.StringNode:
		if len(n.Value) > maxLogExprStringLength {
			return logExprLiteral{}, fmt.Errorf("字符串字面量最多允许 %d 个字符", maxLogExprStringLength)
		}
		return logExprLiteral{kind: logExprFieldString, value: n.Value}, nil
	case *ast.IntegerNode:
		return logExprLiteral{kind: logExprFieldInt, value: n.Value}, nil
	case *ast.FloatNode:
		if math.Trunc(n.Value) != n.Value {
			return logExprLiteral{}, errors.New("整数日志字段不能使用小数字面量")
		}
		return logExprLiteral{kind: logExprFieldInt, value: int(n.Value)}, nil
	case *ast.BoolNode:
		return logExprLiteral{kind: logExprFieldBool, value: n.Value}, nil
	case *ast.NilNode:
		return logExprLiteral{isNil: true}, nil
	case *ast.BuiltinNode:
		if n.Name != "date" {
			return logExprLiteral{}, fmt.Errorf("不支持的函数调用 %q", n.Name)
		}
		value, err := parseLogExprDateCall(n.Arguments)
		if err != nil {
			return logExprLiteral{}, err
		}
		return logExprLiteral{kind: logExprFieldDate, value: value}, nil
	default:
		return logExprLiteral{}, errors.New("只支持字符串、数字、布尔值、nil、date(...) 和字面量数组")
	}
}

func coerceLogExprLiteral(field logExprResolvedField, literal logExprLiteral) (any, error) {
	if field.Timestamp && literal.kind == logExprFieldDate {
		return literal.value.(time.Time).Unix(), nil
	}
	if field.Kind != literal.kind {
		return nil, fmt.Errorf("字段 %q 的值类型不匹配", field.Name)
	}
	return literal.value, nil
}

func parseLogExprDateCall(args []ast.Node) (time.Time, error) {
	if len(args) < 1 || len(args) > 3 {
		return time.Time{}, errors.New("date(...) 需要 1 到 3 个字符串参数")
	}

	dateValue, err := logExprStringArg(args[0])
	if err != nil {
		return time.Time{}, err
	}

	loc := time.Local
	layout := ""
	if len(args) == 2 {
		secondArg, err := logExprStringArg(args[1])
		if err != nil {
			return time.Time{}, err
		}
		if timezoneLoc, err := time.LoadLocation(secondArg); err == nil {
			loc = timezoneLoc
		} else {
			layout = secondArg
		}
	} else if len(args) == 3 {
		layout, err = logExprStringArg(args[1])
		if err != nil {
			return time.Time{}, err
		}
		timezone, err := logExprStringArg(args[2])
		if err != nil {
			return time.Time{}, err
		}
		loc, err = time.LoadLocation(timezone)
		if err != nil {
			return time.Time{}, fmt.Errorf("date(...) 时区无效: %w", err)
		}
	}

	if layout != "" {
		t, err := time.ParseInLocation(layout, dateValue, loc)
		if err != nil {
			return time.Time{}, fmt.Errorf("date(...) 解析失败: %w", err)
		}
		return t, nil
	}

	layouts := []string{
		"2006-01-02",
		"15:04:05",
		"2006-01-02 15:04:05",
		time.RFC3339,
		time.RFC822,
		time.RFC850,
		time.RFC1123,
	}
	for _, layout := range layouts {
		t, err := time.ParseInLocation(layout, dateValue, loc)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("date(...) 无法解析日期 %q", dateValue)
}

func logExprStringArg(node ast.Node) (string, error) {
	stringNode, ok := node.(*ast.StringNode)
	if !ok {
		return "", errors.New("date(...) 参数必须是字符串字面量")
	}
	if len(stringNode.Value) > maxLogExprStringLength {
		return "", fmt.Errorf("字符串字面量最多允许 %d 个字符", maxLogExprStringLength)
	}
	return stringNode.Value, nil
}

func combineLogExprSQL(joiner string, left logExprSQL, right logExprSQL) logExprSQL {
	args := make([]any, 0, len(left.args)+len(right.args))
	args = append(args, left.args...)
	args = append(args, right.args...)
	return logExprSQL{
		sql:              fmt.Sprintf("(%s) %s (%s)", left.sql, joiner, right.sql),
		args:             args,
		needsChannelJoin: left.needsChannelJoin || right.needsChannelJoin,
	}
}

func buildLogExprVariables(node ast.Node, now time.Time) map[string]logExprSQL {
	refs := collectLogExprVariableRefs(node)
	if len(refs) == 0 {
		return nil
	}

	variables := make(map[string]logExprSQL, len(refs))
	for name := range refs {
		if variable, ok := compileLogExprDateShortcut(name, now); ok {
			variables[name] = variable
		}
	}
	return variables
}

func collectLogExprVariableRefs(node ast.Node) map[string]struct{} {
	refs := map[string]struct{}{}
	visitLogExprVariableRefs(node, refs)
	if len(refs) == 0 {
		return nil
	}
	return refs
}

func visitLogExprVariableRefs(node ast.Node, refs map[string]struct{}) {
	switch n := node.(type) {
	case nil:
		return
	case *ast.IdentifierNode:
		if _, ok := logExprDateShortcutOffset(n.Value); ok {
			refs[n.Value] = struct{}{}
		}
	case *ast.BinaryNode:
		visitLogExprVariableRefs(n.Left, refs)
		visitLogExprVariableRefs(n.Right, refs)
	case *ast.UnaryNode:
		visitLogExprVariableRefs(n.Node, refs)
	case *ast.ChainNode:
		visitLogExprVariableRefs(n.Node, refs)
	case *ast.MemberNode:
		visitLogExprVariableRefs(n.Node, refs)
		visitLogExprVariableRefs(n.Property, refs)
	case *ast.SliceNode:
		visitLogExprVariableRefs(n.Node, refs)
		visitLogExprVariableRefs(n.From, refs)
		visitLogExprVariableRefs(n.To, refs)
	case *ast.CallNode:
		visitLogExprVariableRefs(n.Callee, refs)
		for _, arg := range n.Arguments {
			visitLogExprVariableRefs(arg, refs)
		}
	case *ast.BuiltinNode:
		for _, arg := range n.Arguments {
			visitLogExprVariableRefs(arg, refs)
		}
		visitLogExprVariableRefs(n.Map, refs)
	case *ast.PredicateNode:
		visitLogExprVariableRefs(n.Node, refs)
	case *ast.ConditionalNode:
		visitLogExprVariableRefs(n.Cond, refs)
		visitLogExprVariableRefs(n.Exp1, refs)
		visitLogExprVariableRefs(n.Exp2, refs)
	case *ast.VariableDeclaratorNode:
		visitLogExprVariableRefs(n.Value, refs)
		visitLogExprVariableRefs(n.Expr, refs)
	case *ast.SequenceNode:
		for _, item := range n.Nodes {
			visitLogExprVariableRefs(item, refs)
		}
	case *ast.ArrayNode:
		for _, item := range n.Nodes {
			visitLogExprVariableRefs(item, refs)
		}
	case *ast.MapNode:
		for _, pair := range n.Pairs {
			visitLogExprVariableRefs(pair, refs)
		}
	case *ast.PairNode:
		visitLogExprVariableRefs(n.Key, refs)
		visitLogExprVariableRefs(n.Value, refs)
	}
}

func cloneLogExprSQL(value logExprSQL) logExprSQL {
	if len(value.args) > 0 {
		value.args = append([]any(nil), value.args...)
	}
	return value
}

func compileLogExprDateShortcut(name string, now time.Time) (logExprSQL, bool) {
	dayOffset, ok := logExprDateShortcutOffset(name)
	if !ok {
		return logExprSQL{}, false
	}

	start, end := logExprDayBounds(now, dayOffset)
	return logExprSQL{
		sql:  "(logs.created_at >= ? AND logs.created_at < ?)",
		args: []any{start, end},
	}, true
}

func logExprDateShortcutOffset(name string) (int, bool) {
	switch name {
	case "today":
		return 0, true
	case "yesterday":
		return -1, true
	default:
		return 0, false
	}
}

func logExprDayBounds(now time.Time, dayOffset int) (int64, int64) {
	loc := time.Local
	if loc == nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, dayOffset)
	end := start.AddDate(0, 0, 1)
	return start.Unix(), end.Unix()
}

func normalizeLogExprOperator(op string) string {
	return strings.TrimSpace(op)
}

func reverseLogExprComparisonOperator(op string) string {
	switch op {
	case ">":
		return "<"
	case ">=":
		return "<="
	case "<":
		return ">"
	case "<=":
		return ">="
	default:
		return op
	}
}

func escapeLogExprLikePattern(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	value = strings.ReplaceAll(value, "_", "!_")
	return value
}

func logExprFieldNeedsChannelJoin(field logExprField) bool {
	return strings.HasPrefix(field.Column, "channels.")
}

func logExprCanJoinChannels() bool {
	return LOG_DB == DB
}

func applyLogFilters(tx *gorm.DB, opts LogQueryOptions) (*gorm.DB, error) {
	tx = applyLogContainsFilter(tx, "logs.model_name", opts.ModelName)
	if opts.IncludeAdminFields {
		tx = applyLogContainsFilter(tx, "logs.username", opts.Username)
	} else if opts.Username != "" {
		tx = tx.Where("logs.username = ?", opts.Username)
	}
	tx = applyLogContainsFilter(tx, "logs.token_name", opts.TokenName)
	if opts.RequestId != "" {
		tx = tx.Where("logs.request_id = ?", opts.RequestId)
	}
	if opts.UpstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", opts.UpstreamRequestId)
	}
	if opts.StartTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", opts.StartTimestamp)
	}
	if opts.EndTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", opts.EndTimestamp)
	}
	if opts.Channel != 0 {
		tx = tx.Where("logs.channel_id = ?", opts.Channel)
	}
	if opts.Group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", opts.Group)
	}
	return applyLogExprFilter(tx, opts.Expr, opts.IncludeAdminFields)
}

func applyLogStatFilters(tx *gorm.DB, opts LogQueryOptions, includeType bool) (*gorm.DB, error) {
	if opts.IncludeAdminFields {
		tx = applyLogContainsFilter(tx, "logs.username", opts.Username)
	} else if opts.Username != "" {
		tx = tx.Where("logs.username = ?", opts.Username)
	}
	tx = applyLogContainsFilter(tx, "logs.token_name", opts.TokenName)
	if opts.RequestId != "" {
		tx = tx.Where("logs.request_id = ?", opts.RequestId)
	}
	if opts.UpstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", opts.UpstreamRequestId)
	}
	if opts.StartTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", opts.StartTimestamp)
	}
	if opts.EndTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", opts.EndTimestamp)
	}
	tx = applyLogContainsFilter(tx, "logs.model_name", opts.ModelName)
	if opts.Channel != 0 {
		tx = tx.Where("logs.channel_id = ?", opts.Channel)
	}
	if opts.Group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", opts.Group)
	}
	if includeType {
		tx = tx.Where("logs.type = ?", LogTypeConsume)
	}
	return applyLogExprFilter(tx, opts.Expr, opts.IncludeAdminFields)
}

func logDBType() string {
	if os.Getenv("LOG_SQL_DSN") != "" {
		return common.LogSqlType
	}
	if common.UsingPostgreSQL {
		return common.DatabaseTypePostgreSQL
	}
	if common.UsingMySQL {
		return common.DatabaseTypeMySQL
	}
	return common.DatabaseTypeSQLite
}

func logCoalesceInt(expr string) string {
	if logDBType() == common.DatabaseTypeMySQL {
		return "ifnull(" + expr + ",0)"
	}
	return "coalesce(" + expr + ",0)"
}

type logExprSQL struct {
	sql              string
	args             []any
	needsChannelJoin bool
}

type logExprResolvedField struct {
	Name             string
	Column           string
	Kind             logExprFieldKind
	Timestamp        bool
	needsChannelJoin bool
}

type logExprLiteral struct {
	kind  logExprFieldKind
	value any
	isNil bool
}
