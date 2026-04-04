package vault

import (
	"strings"
)

const darwinKeychainService = "ori-agent"

type darwinKeychainStore struct {
	runner    commandRunner
	namespace string
}

func newDarwinKeychainStore(runner commandRunner, namespace string) SecretStore {
	return &darwinKeychainStore{runner: runner, namespace: namespace}
}

func (s *darwinKeychainStore) Get(key SecretKey) (string, error) {
	output, err := s.runner.Run("", "security", "find-generic-password", "-s", darwinKeychainService, "-a", s.account(key), "-w")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "could not be found") || strings.Contains(strings.ToLower(err.Error()), "could not find") {
			return "", ErrSecretNotFound
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (s *darwinKeychainStore) Set(key SecretKey, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return s.Delete(key)
	}
	_, err := s.runner.Run("", "security", "add-generic-password", "-U", "-s", darwinKeychainService, "-a", s.account(key), "-w", value)
	return err
}

func (s *darwinKeychainStore) Delete(key SecretKey) error {
	_, err := s.runner.Run("", "security", "delete-generic-password", "-s", darwinKeychainService, "-a", s.account(key))
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "could not find") {
		return nil
	}
	return err
}

func (s *darwinKeychainStore) Status() StoreStatus {
	return StoreStatus{
		Backend:   BackendDarwinKeychain,
		Available: true,
		Writable:  true,
		Locked:    false,
		Message:   "using macOS Keychain",
	}
}

func (s *darwinKeychainStore) account(key SecretKey) string {
	return s.namespace + ":" + string(key)
}
