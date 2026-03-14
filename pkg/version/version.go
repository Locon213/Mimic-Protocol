package version

import "runtime/debug"

// Version represents the application version
var Version = "dev"

// BuildInfo holds build information
var BuildInfo = "development"

func init() {
	// Try to get version from build info
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" || setting.Key == "vcs.time" {
				if Version == "dev" {
					BuildInfo = setting.Value
					break
				}
			}
		}
	}
}

// GetVersion returns the current version string
func GetVersion() string {
	if Version == "dev" {
		return "dev version"
	}
	return Version
}

// GetFullVersion returns full version string with v prefix
func GetFullVersion() string {
	if Version == "dev" {
		return "dev version"
	}
	return Version
}
