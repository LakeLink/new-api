package billingexpr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunExprRejectsNegativeRequestDependentCharge(t *testing.T) {
	exprString := `param("discount") == true ? tier("invalid", -p) : tier("standard", p)`

	standard, _, err := RunExprWithRequest(exprString, TokenParams{P: 1000}, RequestInput{})
	require.NoError(t, err)
	require.Equal(t, float64(1000), standard)

	_, _, err = RunExprWithRequest(exprString, TokenParams{P: 1000}, RequestInput{
		Body: []byte(`{"discount":true}`),
	})
	require.ErrorContains(t, err, "must not be negative")
}

func TestRunExprRejectsNonFiniteCharge(t *testing.T) {
	_, _, err := RunExpr(`tier("invalid", p / 0)`, TokenParams{P: 1000})
	require.ErrorContains(t, err, "must be finite")
}
