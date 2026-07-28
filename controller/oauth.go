package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// providerParams returns map with Provider key for i18n templates
func providerParams(name string) map[string]any {
	return map[string]any{"Provider": name}
}

const oauthStateTTL = 10 * time.Minute

const oauthUsernameSuffixLength = 12

func newOAuthUsername(prefix string) string {
	suffix := common.GetUUID()[:oauthUsernameSuffixLength]
	prefixRunes := []rune(prefix)
	maxPrefixLength := model.UserNameMaxLength - len(suffix)
	if len(prefixRunes) > maxPrefixLength {
		prefixRunes = prefixRunes[:maxPrefixLength]
	}
	return string(prefixRunes) + suffix
}

type oauthStateRequest struct {
	AffiliateCode string `json:"aff"`
}

// GenerateOAuthCode generates a state code for OAuth CSRF protection
func GenerateOAuthCode(c *gin.Context) {
	var req oauthStateRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	affCode := strings.TrimSpace(req.AffiliateCode)
	if err := common.Validate.Var(affCode, "omitempty,alphanum,max=32"); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	session := sessions.Default(c)
	// OAuth state is a bearer CSRF credential. Use the cryptographically random
	// UUID source rather than the general-purpose pseudo-random string helper.
	state := common.GetUUID()
	previousState, _ := session.Get("oauth_state").(string)
	now := time.Now().Unix()
	if err := model.RotateOAuthState(
		previousState,
		state,
		now+int64(oauthStateTTL/time.Second),
	); err != nil {
		common.ApiError(c, err)
		return
	}
	if affCode != "" {
		session.Set("aff", affCode)
	} else {
		session.Delete("aff")
	}
	session.Set("oauth_state", state)
	err := session.Save()
	if err != nil {
		if deleteErr := model.DeleteOAuthState(state); deleteErr != nil {
			common.SysLog("failed to revoke OAuth state after session save error: " + deleteErr.Error())
		}
		common.ApiError(c, err)
		return
	}
	if _, err := model.CleanupExpiredOAuthStates(now); err != nil {
		common.SysLog("failed to clean expired OAuth states: " + err.Error())
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    state,
	})
}

func consumeOAuthState(session sessions.Session, provided string) (bool, error) {
	if provided == "" {
		return false, nil
	}
	stored, ok := session.Get("oauth_state").(string)
	if !ok || stored == "" || stored != provided {
		return false, nil
	}
	claimed, err := model.ConsumeOAuthState(provided, time.Now().Unix())
	if err != nil {
		return false, err
	}
	session.Delete("oauth_state")
	if err := session.Save(); err != nil {
		return false, err
	}
	return claimed, nil
}

// HandleOAuth handles OAuth callback for all standard OAuth providers
func HandleOAuth(c *gin.Context) {
	providerName := c.Param("provider")
	provider := oauth.GetProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgOAuthUnknownProvider),
		})
		return
	}

	session := sessions.Default(c)

	// 1. Validate state (CSRF protection)
	state := c.Query("state")
	validState, err := consumeOAuthState(session, state)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !validState {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgOAuthStateInvalid),
		})
		return
	}

	// 2. Check if user is already logged in (bind flow)
	username := session.Get("username")
	if username != nil {
		currentUser, currentErr := getCurrentSessionUser(c)
		if currentErr == nil {
			handleOAuthBind(c, provider, currentUser)
			return
		}
		if !errors.Is(currentErr, errSessionInvalid) {
			common.ApiError(c, currentErr)
			return
		}
		// A stale signed cookie must not turn an OAuth login into a bind
		// attempt. Clear it and continue through the ordinary OAuth login flow.
		session.Clear()
		if err := session.Save(); err != nil {
			common.ApiErrorI18n(c, i18n.MsgUserSessionSaveFailed)
			return
		}
	}

	// 3. Check if provider is enabled
	if !provider.IsEnabled() {
		common.ApiErrorI18n(c, i18n.MsgOAuthNotEnabled, providerParams(provider.GetName()))
		return
	}

	// 4. Handle error from provider
	errorCode := c.Query("error")
	if errorCode != "" {
		errorDescription := c.Query("error_description")
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": errorDescription,
		})
		return
	}

	// 5. Exchange code for token
	code := c.Query("code")
	token, err := provider.ExchangeToken(c.Request.Context(), code, c)
	if err != nil {
		handleOAuthError(c, err)
		return
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		handleOAuthError(c, oauth.NewOAuthError(
			i18n.MsgOAuthTokenFailed,
			providerParams(provider.GetName()),
		))
		return
	}

	// 6. Get user info
	oauthUser, err := provider.GetUserInfo(c.Request.Context(), token)
	if err != nil {
		handleOAuthError(c, err)
		return
	}
	if oauthUser == nil ||
		strings.TrimSpace(oauthUser.ProviderUserID) == "" {
		handleOAuthError(c, oauth.NewOAuthError(
			i18n.MsgOAuthUserInfoEmpty,
			providerParams(provider.GetName()),
		))
		return
	}

	// 7. Find or create user
	user, err := findOrCreateOAuthUser(c, provider, oauthUser, session)
	if err != nil {
		if errors.Is(err, model.ErrEmailAlreadyTaken) {
			common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
			return
		}
		switch err.(type) {
		case *OAuthUserDeletedError:
			common.ApiErrorI18n(c, i18n.MsgOAuthUserDeleted)
		case *OAuthRegistrationDisabledError:
			common.ApiErrorI18n(c, i18n.MsgUserRegisterDisabled)
		case *OAuthEmailAlreadyTakenError:
			common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
		default:
			common.ApiError(c, err)
		}
		return
	}

	// 8. Check user status
	if user.Status != common.UserStatusEnabled {
		common.ApiErrorI18n(c, i18n.MsgOAuthUserBanned)
		return
	}

	// 9. Setup login
	setupLogin(user, c)
}

// handleOAuthBind handles binding OAuth account to existing user
func handleOAuthBind(
	c *gin.Context,
	provider oauth.Provider,
	user *model.User,
) {
	if !provider.IsEnabled() {
		common.ApiErrorI18n(c, i18n.MsgOAuthNotEnabled, providerParams(provider.GetName()))
		return
	}
	if user == nil {
		common.ApiErrorI18n(c, i18n.MsgAuthNotLoggedIn)
		return
	}
	if !requireAnySecureVerification(c) {
		return
	}

	// Exchange code for token
	code := c.Query("code")
	token, err := provider.ExchangeToken(c.Request.Context(), code, c)
	if err != nil {
		handleOAuthError(c, err)
		return
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		handleOAuthError(c, oauth.NewOAuthError(
			i18n.MsgOAuthTokenFailed,
			providerParams(provider.GetName()),
		))
		return
	}

	// Get user info
	oauthUser, err := provider.GetUserInfo(c.Request.Context(), token)
	if err != nil {
		handleOAuthError(c, err)
		return
	}
	if oauthUser == nil ||
		strings.TrimSpace(oauthUser.ProviderUserID) == "" {
		handleOAuthError(c, oauth.NewOAuthError(
			i18n.MsgOAuthUserInfoEmpty,
			providerParams(provider.GetName()),
		))
		return
	}

	// Check if this OAuth account is already bound (check both new ID and legacy ID)
	taken, err := provider.IsUserIDTaken(oauthUser.ProviderUserID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if taken {
		common.ApiErrorI18n(c, i18n.MsgOAuthAlreadyBound, providerParams(provider.GetName()))
		return
	}
	// Also check legacy ID to prevent duplicate bindings during migration period
	if legacyID, ok := oauthUser.Extra["legacy_id"].(string); ok && legacyID != "" {
		taken, err := provider.IsUserIDTaken(legacyID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if taken {
			common.ApiErrorI18n(c, i18n.MsgOAuthAlreadyBound, providerParams(provider.GetName()))
			return
		}
	}

	// Handle binding based on provider type
	if genericProvider, ok := provider.(*oauth.GenericOAuthProvider); ok {
		// Custom provider: use user_oauth_bindings table
		err = model.UpdateUserOAuthBinding(user.Id, genericProvider.GetProviderId(), oauthUser.ProviderUserID)
		if err != nil {
			var conflict *model.UserOAuthBindingConflictError
			if errors.As(err, &conflict) {
				common.ApiErrorI18n(c, i18n.MsgOAuthAlreadyBound, providerParams(provider.GetName()))
				return
			}
			common.ApiError(c, err)
			return
		}
	} else {
		// Built-in provider: claim the normalized identity and update its legacy
		// user column in one transaction.
		providerKey := strings.TrimSuffix(provider.GetProviderPrefix(), "_")
		err = model.DB.Transaction(func(tx *gorm.DB) error {
			return model.BindBuiltInOAuthIdentityWithTx(
				tx,
				providerKey,
				oauthUser.ProviderUserID,
				user.Id,
			)
		})
		if err != nil {
			var conflict *model.BuiltInOAuthIdentityConflictError
			if errors.As(err, &conflict) {
				common.ApiErrorI18n(c, i18n.MsgOAuthAlreadyBound, providerParams(provider.GetName()))
				return
			}
			common.ApiError(c, err)
			return
		}
	}

	common.ApiSuccessI18n(c, i18n.MsgOAuthBindSuccess, gin.H{
		"action": "bind",
	})
}

// findOrCreateOAuthUser finds existing user or creates new user
func findOrCreateOAuthUser(c *gin.Context, provider oauth.Provider, oauthUser *oauth.OAuthUser, session sessions.Session) (*model.User, error) {
	if provider == nil || oauthUser == nil ||
		strings.TrimSpace(oauthUser.ProviderUserID) == "" {
		return nil, oauth.NewOAuthError(i18n.MsgOAuthUserInfoEmpty, nil)
	}
	user := &model.User{}

	// Check if user already exists with new ID
	taken, err := provider.IsUserIDTaken(oauthUser.ProviderUserID)
	if err != nil {
		return nil, err
	}
	if taken {
		err := provider.FillUserByProviderID(user, oauthUser.ProviderUserID)
		if err != nil {
			return nil, err
		}
		// Check if user has been deleted
		if user.Id == 0 {
			return nil, &OAuthUserDeletedError{}
		}
		return user, nil
	}

	// Try to find user with legacy ID (for GitHub migration from login to numeric ID)
	if legacyID, ok := oauthUser.Extra["legacy_id"].(string); ok && legacyID != "" {
		taken, err := provider.IsUserIDTaken(legacyID)
		if err != nil {
			return nil, err
		}
		if taken {
			err := provider.FillUserByProviderID(user, legacyID)
			if err != nil {
				return nil, err
			}
			if user.Id != 0 {
				// Found user with legacy ID, migrate to new ID
				common.SysLog(fmt.Sprintf("[OAuth] Migrating legacy identity for user %d", user.Id))
				if err := user.UpdateGitHubId(oauthUser.ProviderUserID); err != nil {
					return nil, fmt.Errorf("migrate GitHub OAuth identity for user %d: %w", user.Id, err)
				}
				return user, nil
			}
		}
	}

	// User doesn't exist, create new user if registration is enabled
	if !common.GetLegacyOptionBool("RegisterEnabled", &common.RegisterEnabled) {
		return nil, &OAuthRegistrationDisabledError{}
	}

	// Set up new user
	user.Username = newOAuthUsername(provider.GetProviderPrefix())

	if oauthUser.Username != "" {
		if exists, err := model.CheckUserExistOrDeleted(oauthUser.Username, ""); err == nil && !exists {
			// 防止索引退化
			if len(oauthUser.Username) <= model.UserNameMaxLength {
				user.Username = oauthUser.Username
			}
		}
	}

	if oauthUser.DisplayName != "" {
		user.DisplayName = oauthUser.DisplayName
	} else if oauthUser.Username != "" {
		user.DisplayName = oauthUser.Username
	} else {
		user.DisplayName = provider.GetName() + " User"
	}
	if oauthUser.Email != "" {
		user.Email = model.NormalizeEmail(oauthUser.Email)
		if err := model.EnsureEmailAvailable(user.Email, 0); err != nil {
			if errors.Is(err, model.ErrEmailAlreadyTaken) {
				return nil, &OAuthEmailAlreadyTakenError{}
			}
			return nil, err
		}
	}
	user.Role = common.RoleCommonUser
	user.Status = common.UserStatusEnabled

	// Handle affiliate code
	inviterId := 0
	if affCode, ok := session.Get("aff").(string); ok && affCode != "" {
		inviterId, _ = model.GetUserIdByAffCode(affCode)
	}
	session.Delete("aff")
	if err := session.Save(); err != nil {
		return nil, err
	}

	// Use transaction to ensure user creation and OAuth binding are atomic
	if genericProvider, ok := provider.(*oauth.GenericOAuthProvider); ok {
		// Custom provider: create user and binding in a transaction
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// Create user
			if err := user.InsertWithTx(tx, inviterId); err != nil {
				return err
			}

			// Create OAuth binding
			binding := &model.UserOAuthBinding{
				UserId:         user.Id,
				ProviderId:     genericProvider.GetProviderId(),
				ProviderUserId: oauthUser.ProviderUserID,
			}
			if err := model.CreateUserOAuthBindingWithTx(tx, binding); err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			var conflict *model.UserOAuthBindingConflictError
			if errors.As(err, &conflict) {
				winner := &model.User{}
				if fillErr := provider.FillUserByProviderID(
					winner,
					oauthUser.ProviderUserID,
				); fillErr != nil {
					return nil, fillErr
				}
				if winner.Id == 0 {
					return nil, &OAuthUserDeletedError{}
				}
				return winner, nil
			}
			return nil, err
		}

		// Perform post-transaction tasks (logs, sidebar config, inviter rewards)
		user.FinalizeOAuthUserCreation(inviterId)
	} else {
		// Built-in provider: create user and update provider ID in a transaction
		providerKey := strings.TrimSuffix(provider.GetProviderPrefix(), "_")
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// Create user
			if err := user.InsertWithTx(tx, inviterId); err != nil {
				return err
			}

			return model.BindBuiltInOAuthIdentityWithTx(
				tx,
				providerKey,
				oauthUser.ProviderUserID,
				user.Id,
			)
		})
		if err != nil {
			var conflict *model.BuiltInOAuthIdentityConflictError
			if errors.As(err, &conflict) {
				winner := &model.User{}
				if fillErr := provider.FillUserByProviderID(winner, oauthUser.ProviderUserID); fillErr != nil {
					return nil, fillErr
				}
				if winner.Id == 0 {
					return nil, &OAuthUserDeletedError{}
				}
				return winner, nil
			}
			return nil, err
		}

		// Perform post-transaction tasks
		user.FinalizeOAuthUserCreation(inviterId)
	}

	return user, nil
}

// Error types for OAuth
type OAuthUserDeletedError struct{}

func (e *OAuthUserDeletedError) Error() string {
	return "user has been deleted"
}

type OAuthRegistrationDisabledError struct{}

func (e *OAuthRegistrationDisabledError) Error() string {
	return "registration is disabled"
}

type OAuthEmailAlreadyTakenError struct{}

func (e *OAuthEmailAlreadyTakenError) Error() string {
	return "email is already in use"
}

// handleOAuthError handles OAuth errors and returns translated message
func handleOAuthError(c *gin.Context, err error) {
	switch e := err.(type) {
	case *oauth.OAuthError:
		if e.Params != nil {
			common.ApiErrorI18n(c, e.MsgKey, e.Params)
		} else {
			common.ApiErrorI18n(c, e.MsgKey)
		}
	case *oauth.AccessDeniedError:
		common.ApiErrorMsg(c, e.Message)
	case *oauth.TrustLevelError:
		common.ApiErrorI18n(c, i18n.MsgOAuthTrustLevelLow)
	default:
		common.ApiError(c, err)
	}
}
