// Package maConf reads and writes the configurations.
package maConf

import (
	"io/ioutil"
	"os"

	"gopkg.in/ini.v1"
)

const (
	// VERSION contains the mahou version.
	VERSION = "0.0.1"
	// PATH contains relative or absolute default path to the configuration
	// file.
	PATH = "mahou.conf"
)

// ReadConfig reads the configuration file.
func ReadConfig(path string) (*Config, error) {
	cfg, err := ini.InsensitiveLoad(path)

	conf := &Config{path: path}
	conf.Login, conf.Prefs = new(Login), new(Preferences)

	err = cfg.Section("login").MapTo(conf.Login)
	if err != nil {
		return conf, err
	}

	err = cfg.Section("mahou").MapTo(conf.Prefs)

	return conf, err
}

// PopulateConfig copies the sample config to <path>.
func PopulateConfig(file string) error {
	config := `# This is the configuration file for mahou, a Matrix echo
# testing bot.
# See https://github.com/26000/mahou for help. Also join
# https://matrix.to/#/#mahou:kotobank.ch .
#
# Enter your bot login info. Don't worry, the password will be redacted and
# replaced with an accesstoken (which you don't need to enter).
[login]
localpart = mahouBot
password =
homeserver = https://matrix.org

# You don't need to enter these
userid =
accesstoken =


# General mahou settings.
[mahou]

# The path to your database. It is a file-based LevelDB, so everything you
# need is a path to a directory which you can write to. Can be relative to CWD
# or absolute.
db = data/
`
	return ioutil.WriteFile(file, []byte(config), os.FileMode(0600))
}

// Config represents the whole mahou configuration, containing all the
// needed substructures.
type Config struct {
	Login *Login
	Prefs *Preferences
	path  string
}

// UpdateCredentials writes access token to the config and redacts
// localpart and password.
func (conf *Config) UpdateCredentials(userID string, accessToken string,
	homeServer string) error {
	cfg, err := ini.InsensitiveLoad(conf.path)
	if err != nil {
		return err
	}

	cfg.Section("login").Key("password").SetValue("")
	cfg.Section("login").Key("homeserver").SetValue(homeServer)

	cfg.Section("login").Key("userid").SetValue(userID)
	cfg.Section("login").Key("accesstoken").SetValue(accessToken)
	return cfg.SaveTo(conf.path)
}

// Login represents the information needed to log into a Matrix server.
type Login struct {
	Localpart  string
	Password   string
	HomeServer string

	UserID      string
	AccessToken string
}

// Preferences represents settings that are not required.
type Preferences struct {
}
