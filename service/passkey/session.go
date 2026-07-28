package passkey

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	webauthn "github.com/go-webauthn/webauthn/webauthn"
)

var errSessionNotFound = errors.New("Passkey 会话不存在或已过期")

func SaveSessionData(c *gin.Context, key string, data *webauthn.SessionData) error {
	session := sessions.Default(c)
	previousChallenge := challengeFromSessionValue(session.Get(key))
	if data == nil {
		if previousChallenge != "" {
			if err := model.DeleteAuthenticationToken(key, previousChallenge); err != nil {
				return err
			}
		}
		session.Delete(key)
		return session.Save()
	}
	if data.Challenge == "" || data.Expires.IsZero() {
		return errors.New("Passkey 会话格式无效")
	}
	payload, err := common.Marshal(data)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if err := model.RotateAuthenticationToken(
		key,
		previousChallenge,
		data.Challenge,
		data.Expires.UnixMilli(),
		now,
	); err != nil {
		return err
	}
	session.Set(key, string(payload))
	if err := session.Save(); err != nil {
		if deleteErr := model.DeleteAuthenticationToken(
			key,
			data.Challenge,
		); deleteErr != nil {
			common.SysLog("failed to revoke Passkey challenge after session save error: " + deleteErr.Error())
		}
		return err
	}
	return nil
}

func PopSessionData(c *gin.Context, key string) (*webauthn.SessionData, error) {
	session := sessions.Default(c)
	raw := session.Get(key)
	if raw == nil {
		return nil, errSessionNotFound
	}
	var data webauthn.SessionData
	switch value := raw.(type) {
	case string:
		if err := common.UnmarshalJsonStr(value, &data); err != nil {
			return nil, err
		}
	case []byte:
		if err := common.Unmarshal(value, &data); err != nil {
			return nil, err
		}
	default:
		session.Delete(key)
		_ = session.Save()
		return nil, errors.New("Passkey 会话格式无效")
	}
	claimed, err := model.ConsumeAuthenticationToken(
		key,
		data.Challenge,
		time.Now().UnixMilli(),
	)
	session.Delete(key)
	if saveErr := session.Save(); saveErr != nil {
		return nil, saveErr
	}
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, errSessionNotFound
	}
	return &data, nil
}

func challengeFromSessionValue(raw any) string {
	if raw == nil {
		return ""
	}
	var data webauthn.SessionData
	switch value := raw.(type) {
	case string:
		if err := common.UnmarshalJsonStr(value, &data); err != nil {
			return ""
		}
	case []byte:
		if err := common.Unmarshal(value, &data); err != nil {
			return ""
		}
	default:
		return ""
	}
	return data.Challenge
}
