package config

import "testing"

func TestHubProxyOptOut(t *testing.T) {
	for _, value := range []string{"", "on", "off", "false", "0"} {
		t.Run(value, func(t *testing.T) {
			clearKonveyorEnv(t)
			setRequiredEnv(t)
			t.Setenv("HARNESS_HUB_PROXY", value)
			cfg, err := LoadFromEnv()
			if err != nil {
				t.Fatal(err)
			}
			want := value != "off" && value != "false" && value != "0"
			if cfg.HubProxy != want {
				t.Errorf("HubProxy=%v, want %v", cfg.HubProxy, want)
			}
		})
	}
}
