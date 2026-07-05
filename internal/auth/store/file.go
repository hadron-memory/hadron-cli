package store

import (
	"encoding/json"
	"os"

	"github.com/hadron-memory/hadron-cli/internal/config"
)

// File stores tokens in ~/.config/hadron/auth.json with 0600 perms.
type File struct{}

type authFile struct {
	Hosts map[string]hostAuth `json:"hosts"`
}

type hostAuth struct {
	Token string `json:"token"`
}

func (File) Name() string { return "file" }

func (File) Get(host string) (string, error) {
	data, err := readAuthFile()
	if err != nil {
		return "", err
	}
	entry, ok := data.Hosts[host]
	if !ok || entry.Token == "" {
		return "", ErrNotFound
	}
	return entry.Token, nil
}

func (File) Set(host, token string) error {
	// Lock the whole read-modify-write so two concurrent logins can't each read
	// the file before the other writes and silently drop one host's token (#118).
	return config.WithLock(func() error {
		data, err := readAuthFile()
		if err != nil {
			return err
		}
		data.Hosts[host] = hostAuth{Token: token}
		return writeAuthFile(data)
	})
}

func (File) Delete(host string) error {
	return config.WithLock(func() error {
		data, err := readAuthFile()
		if err != nil {
			return err
		}
		if _, ok := data.Hosts[host]; !ok {
			return ErrNotFound
		}
		delete(data.Hosts, host)
		return writeAuthFile(data)
	})
}

func readAuthFile() (*authFile, error) {
	data := &authFile{Hosts: map[string]hostAuth{}}
	path, err := config.AuthFile()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, data); err != nil {
		return nil, err
	}
	if data.Hosts == nil {
		data.Hosts = map[string]hostAuth{}
	}
	return data, nil
}

func writeAuthFile(data *authFile) error {
	if _, err := config.EnsureDir(); err != nil {
		return err
	}
	path, err := config.AuthFile()
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(path, append(raw, '\n'), 0o600)
}
