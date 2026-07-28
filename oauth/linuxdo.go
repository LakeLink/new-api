package oauth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func init() {
	Register("linuxdo", &LinuxDOProvider{})
}

// LinuxDOProvider implements OAuth for Linux DO
type LinuxDOProvider struct{}

type linuxdoUser struct {
	Id         int    `json:"id"`
	Username   string `json:"username"`
	Name       string `json:"name"`
	Active     bool   `json:"active"`
	TrustLevel int    `json:"trust_level"`
	Silenced   bool   `json:"silenced"`
}

func (p *LinuxDOProvider) GetName() string {
	return "Linux DO"
}

func (p *LinuxDOProvider) IsEnabled() bool {
	return common.GetLegacyOptionBool("LinuxDOOAuthEnabled", &common.LinuxDOOAuthEnabled)
}

func (p *LinuxDOProvider) ExchangeToken(ctx context.Context, code string, c *gin.Context) (*OAuthToken, error) {
	if code == "" {
		return nil, NewOAuthError(i18n.MsgOAuthInvalidCode, nil)
	}

	logger.LogDebug(ctx, "[OAuth-LinuxDO] ExchangeToken: started")

	// Get access token using Basic auth
	tokenEndpoint := common.GetEnvOrDefaultString("LINUX_DO_TOKEN_ENDPOINT", "https://connect.linux.do/oauth2/token")
	clientID := common.GetLegacyOptionString("LinuxDOClientId", &common.LinuxDOClientId)
	clientSecret := common.GetLegacyOptionString("LinuxDOClientSecret", &common.LinuxDOClientSecret)
	credentials := clientID + ":" + clientSecret
	basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials))

	// Get redirect URI from request
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	redirectURI := fmt.Sprintf("%s://%s/api/oauth/linuxdo", scheme, c.Request.Host)

	logger.LogDebug(ctx, "[OAuth-LinuxDO] ExchangeToken: token_endpoint=%s, redirect_uri=%s",
		common.MaskSensitiveInfo(tokenEndpoint), common.MaskSensitiveInfo(redirectURI))

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, service.SanitizeNetworkError(err)
	}
	req.Header.Set("Authorization", basicAuth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := protectedOAuthHTTPClient(5 * time.Second)
	res, err := service.DoUpstreamRequest(client, req)
	if err != nil {
		safeError := common.MaskSensitiveInfo(err.Error())
		logger.LogError(ctx, fmt.Sprintf("[OAuth-LinuxDO] ExchangeToken error: %s", safeError))
		return nil, NewOAuthErrorWithRaw(i18n.MsgOAuthConnectFailed, map[string]any{"Provider": "Linux DO"}, safeError)
	}
	defer res.Body.Close()

	logger.LogDebug(ctx, "[OAuth-LinuxDO] ExchangeToken response status: %d", res.StatusCode)
	if err := requireOAuthSuccessStatus(res); err != nil {
		logger.LogError(ctx, fmt.Sprintf("[OAuth-LinuxDO] ExchangeToken failed: status=%d", res.StatusCode))
		return nil, NewOAuthError(i18n.MsgOAuthTokenFailed, map[string]any{"Provider": "Linux DO"})
	}

	var tokenRes struct {
		AccessToken string `json:"access_token"`
		Message     string `json:"message"`
	}
	if err := decodeOAuthJSONResponse(res, &tokenRes); err != nil {
		logger.LogError(ctx, fmt.Sprintf("[OAuth-LinuxDO] ExchangeToken decode error: %s", err.Error()))
		return nil, err
	}

	if tokenRes.AccessToken == "" {
		logger.LogError(ctx, "[OAuth-LinuxDO] ExchangeToken failed: empty access token")
		return nil, NewOAuthError(i18n.MsgOAuthTokenFailed, map[string]any{"Provider": "Linux DO"})
	}

	logger.LogDebug(ctx, "[OAuth-LinuxDO] ExchangeToken success")

	return &OAuthToken{
		AccessToken: tokenRes.AccessToken,
	}, nil
}

func (p *LinuxDOProvider) GetUserInfo(ctx context.Context, token *OAuthToken) (*OAuthUser, error) {
	userEndpoint := common.GetEnvOrDefaultString("LINUX_DO_USER_ENDPOINT", "https://connect.linux.do/api/user")

	logger.LogDebug(ctx, "[OAuth-LinuxDO] GetUserInfo: user_endpoint=%s", common.MaskSensitiveInfo(userEndpoint))

	req, err := http.NewRequestWithContext(ctx, "GET", userEndpoint, nil)
	if err != nil {
		return nil, service.SanitizeNetworkError(err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")

	client := protectedOAuthHTTPClient(5 * time.Second)
	res, err := service.DoUpstreamRequest(client, req)
	if err != nil {
		safeError := common.MaskSensitiveInfo(err.Error())
		logger.LogError(ctx, fmt.Sprintf("[OAuth-LinuxDO] GetUserInfo error: %s", safeError))
		return nil, NewOAuthErrorWithRaw(i18n.MsgOAuthConnectFailed, map[string]any{"Provider": "Linux DO"}, safeError)
	}
	defer res.Body.Close()

	logger.LogDebug(ctx, "[OAuth-LinuxDO] GetUserInfo response status: %d", res.StatusCode)
	if err := requireOAuthSuccessStatus(res); err != nil {
		logger.LogError(ctx, fmt.Sprintf("[OAuth-LinuxDO] GetUserInfo failed: status=%d", res.StatusCode))
		return nil, NewOAuthError(i18n.MsgOAuthGetUserErr, map[string]any{"Provider": "Linux DO"})
	}

	var linuxdoUser linuxdoUser
	if err := decodeOAuthJSONResponse(res, &linuxdoUser); err != nil {
		logger.LogError(ctx, fmt.Sprintf("[OAuth-LinuxDO] GetUserInfo decode error: %s", err.Error()))
		return nil, err
	}

	if linuxdoUser.Id == 0 {
		logger.LogError(ctx, "[OAuth-LinuxDO] GetUserInfo failed: invalid user id")
		return nil, NewOAuthError(i18n.MsgOAuthUserInfoEmpty, map[string]any{"Provider": "Linux DO"})
	}

	logger.LogDebug(ctx, "[OAuth-LinuxDO] GetUserInfo success: trust_level=%d, active=%v, silenced=%v",
		linuxdoUser.TrustLevel, linuxdoUser.Active, linuxdoUser.Silenced)

	// Check trust level
	minimumTrustLevel := common.GetLegacyOptionInt("LinuxDOMinimumTrustLevel", &common.LinuxDOMinimumTrustLevel)
	if linuxdoUser.TrustLevel < minimumTrustLevel {
		logger.LogWarn(ctx, fmt.Sprintf("[OAuth-LinuxDO] GetUserInfo: trust level too low (required=%d, current=%d)",
			minimumTrustLevel, linuxdoUser.TrustLevel))
		return nil, &TrustLevelError{
			Required: minimumTrustLevel,
			Current:  linuxdoUser.TrustLevel,
		}
	}

	logger.LogDebug(ctx, "[OAuth-LinuxDO] GetUserInfo success")

	return &OAuthUser{
		ProviderUserID: strconv.Itoa(linuxdoUser.Id),
		Username:       linuxdoUser.Username,
		DisplayName:    linuxdoUser.Name,
		Extra: map[string]any{
			"trust_level": linuxdoUser.TrustLevel,
			"active":      linuxdoUser.Active,
			"silenced":    linuxdoUser.Silenced,
		},
	}, nil
}

func (p *LinuxDOProvider) IsUserIDTaken(providerUserID string) (bool, error) {
	return model.IsLinuxDOIdAlreadyTaken(providerUserID)
}

func (p *LinuxDOProvider) FillUserByProviderID(user *model.User, providerUserID string) error {
	user.LinuxDOId = providerUserID
	return user.FillUserByLinuxDOId()
}

func (p *LinuxDOProvider) SetProviderUserID(user *model.User, providerUserID string) {
	user.LinuxDOId = providerUserID
}

func (p *LinuxDOProvider) GetProviderPrefix() string {
	return "linuxdo_"
}

// TrustLevelError indicates the user's trust level is too low
type TrustLevelError struct {
	Required int
	Current  int
}

func (e *TrustLevelError) Error() string {
	return "trust level too low"
}
