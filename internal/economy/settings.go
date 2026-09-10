package economy

// SettingsReader is the narrow slice of the app's settings the economy needs.
// The config manager satisfies it; the interface keeps this package free of a
// dependency on config, and keeps the service testable with two booleans.
type SettingsReader interface {
	EconomyCreativeMode() bool
	EconomyDailyEnergyTokens() int64
}

// SettingsFunc adapts a pair of closures to SettingsReader, so the server can
// wire the live config manager without either side gaining a type.
//
// Both fields are read on every call rather than captured once, because the user
// can flip creative mode in Settings while Home is open and the next quote has
// to reflect it.
type SettingsFunc struct {
	CreativeMode      func() bool
	DailyEnergyTokens func() int64
}

func (s SettingsFunc) EconomyCreativeMode() bool {
	if s.CreativeMode == nil {
		return false
	}
	return s.CreativeMode()
}

func (s SettingsFunc) EconomyDailyEnergyTokens() int64 {
	if s.DailyEnergyTokens == nil {
		return DefaultDailyEnergyTokens
	}
	figure := s.DailyEnergyTokens()
	if figure <= 0 {
		return DefaultDailyEnergyTokens
	}
	return figure
}

// EnergyFunc adapts a closure to EnergySource, for the same reason.
type EnergyFunc func() int64

func (f EnergyFunc) TokensUsedToday() int64 {
	if f == nil {
		return 0
	}
	return f()
}
