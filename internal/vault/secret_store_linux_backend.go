package vault

import (
	"strings"
)

const linuxSecretServiceName = "ori-agent"

type linuxSecretServiceStore struct {
	runner    commandRunner
	namespace string
}

func newLinuxSecretServiceStore(runner commandRunner, namespace string) SecretStore {
	return &linuxSecretServiceStore{runner: runner, namespace: namespace}
}

func (s *linuxSecretServiceStore) Get(key SecretKey) (string, error) {
	output, err := s.runner.Run("", "secret-tool", "lookup", "service", linuxSecretServiceName, "namespace", s.namespace, "account", string(key))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", ErrSecretNotFound
	}
	return value, nil
}

func (s *linuxSecretServiceStore) Set(key SecretKey, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return s.Delete(key)
	}
	_, err := s.runner.Run(value, "secret-tool", "store", "--label", "Ori Agent "+string(key), "service", linuxSecretServiceName, "namespace", s.namespace, "account", string(key))
	return err
}

func (s *linuxSecretServiceStore) Delete(key SecretKey) error {
	_, err := s.runner.Run("", "secret-tool", "clear", "service", linuxSecretServiceName, "namespace", s.namespace, "account", string(key))
	return err
}

func (s *linuxSecretServiceStore) Status() StoreStatus {
	return StoreStatus{
		Backend:   BackendLinuxSecretService,
		Available: true,
		Writable:  true,
		Locked:    false,
		Message:   "using Linux Secret Service",
	}
}
