// Package brand holds every product-branding string: the product and
// command names, the rules file name, and the configuration names. A rebrand
// edits only this file.
package brand

const (
	// Name is the product's display name.
	Name = "Kit"
	// Command is the CLI command name users type.
	Command = "kit"
	// RulesFileName is the name every rules file has, in any directory.
	RulesFileName = "KIT"
	// ConfigName is the configuration file's base name, without extension,
	// looked up in $HOME.
	ConfigName = ".kit"
	// EnvPrefix prefixes every configuration environment variable.
	EnvPrefix = "KIT_"
	// EnvConfig names the environment variable holding an explicit
	// configuration file path.
	EnvConfig = EnvPrefix + "CONFIG"
)
