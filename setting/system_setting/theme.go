package system_setting

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type ThemeSettings struct {
	Frontend string `json:"frontend"`
}

var themeSettings = ThemeSettings{
	Frontend: "classic",
}

func (s ThemeSettings) Validate() error {
	switch s.Frontend {
	case "classic", "default":
		return nil
	default:
		return fmt.Errorf("frontend theme must be classic or default")
	}
}

func init() {
	config.GlobalConfig.Register("theme", &themeSettings)
	syncThemeToCommon()
}

func syncThemeToCommon() {
	common.SetTheme(GetThemeSettings().Frontend)
}

func GetThemeSettings() *ThemeSettings {
	return config.Snapshot[ThemeSettings]("theme")
}

// UpdateAndSyncTheme syncs the theme config to common after DB load.
func UpdateAndSyncTheme() {
	syncThemeToCommon()
}
