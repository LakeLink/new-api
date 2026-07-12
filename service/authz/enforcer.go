package authz

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	"gorm.io/gorm"
)

var (
	enforcerMu      sync.RWMutex
	enforcer        *casbin.SyncedEnforcer
	failClosedMu    sync.RWMutex
	failClosedUsers = make(map[int]uint64)
	failClosedEpoch uint64
)

const modelText = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act, eft

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = r.sub == p.sub && r.obj == p.obj && r.act == p.act && p.eft == "allow"
`

func Init(db *gorm.DB) error {
	if common.IsMasterNode {
		if err := seedBuiltInRoles(db); err != nil {
			return err
		}
		if err := resetBuiltInRolePolicies(db); err != nil {
			return err
		}
	}

	m, err := casbinmodel.NewModelFromString(modelText)
	if err != nil {
		return err
	}
	e, err := casbin.NewSyncedEnforcer(m, newGormAdapter(db))
	if err != nil {
		return err
	}
	e.EnableAutoSave(true)

	enforcerMu.Lock()
	enforcer = e
	enforcerMu.Unlock()
	resetFailClosedUsers()

	if !common.IsMasterNode {
		return nil
	}
	return seedDefaultPolicies()
}

func currentEnforcer() *casbin.SyncedEnforcer {
	enforcerMu.RLock()
	defer enforcerMu.RUnlock()
	return enforcer
}

func ReloadPolicy() error {
	enforcerMu.Lock()
	defer enforcerMu.Unlock()
	if enforcer == nil {
		return fmt.Errorf("authz enforcer is not initialized")
	}
	failClosedSnapshot := snapshotFailClosedUsers()
	if err := enforcer.LoadPolicy(); err != nil {
		return err
	}
	clearReloadedFailClosedUsers(failClosedSnapshot)
	return nil
}

// MarkUserFailClosed denies all managed permissions for a user until a full
// policy reload succeeds. Callers use this before reloading after a committed
// authorization mutation so a transient adapter failure cannot leave stale
// grants active in the old in-memory policy snapshot.
func MarkUserFailClosed(userID int) {
	if userID <= 0 {
		return
	}
	failClosedMu.Lock()
	failClosedEpoch++
	failClosedUsers[userID] = failClosedEpoch
	failClosedMu.Unlock()
}

func isUserFailClosed(userID int) bool {
	failClosedMu.RLock()
	_, denied := failClosedUsers[userID]
	failClosedMu.RUnlock()
	return denied
}

func snapshotFailClosedUsers() map[int]uint64 {
	failClosedMu.RLock()
	snapshot := make(map[int]uint64, len(failClosedUsers))
	for userID, epoch := range failClosedUsers {
		snapshot[userID] = epoch
	}
	failClosedMu.RUnlock()
	return snapshot
}

func clearReloadedFailClosedUsers(snapshot map[int]uint64) {
	failClosedMu.Lock()
	for userID, epoch := range snapshot {
		if failClosedUsers[userID] == epoch {
			delete(failClosedUsers, userID)
		}
	}
	failClosedMu.Unlock()
}

func resetFailClosedUsers() {
	failClosedMu.Lock()
	clear(failClosedUsers)
	failClosedEpoch = 0
	failClosedMu.Unlock()
}

// StartPolicySync periodically reloads the authorization policy from the database.
// The enforcer keeps an in-memory snapshot, and permission changes are written
// straight to the DB (see SetUserPermissionsInTx) with only the local node's
// snapshot refreshed afterwards. Without this loop other instances in a
// multi-node deployment would keep serving stale permissions (including not
// honoring a revoked grant) until restart. Mirrors model.SyncOptions polling.
func StartPolicySync(frequency int) {
	if frequency <= 0 {
		return
	}
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		if err := ReloadPolicy(); err != nil {
			common.SysError("failed to reload authz policy: " + err.Error())
		}
	}
}
