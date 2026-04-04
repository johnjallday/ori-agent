package vault

func newWindowsSecretStore(namespace string) SecretStore {
	return unavailableStore{
		status: StoreStatus{
			Backend:   BackendWindowsSecureStore,
			Available: false,
			Writable:  false,
			Locked:    true,
			Message:   "Windows secure storage backend is not implemented yet; configure a vault passphrase fallback instead",
		},
		err: ErrSecretStoreUnsupported,
	}
}
