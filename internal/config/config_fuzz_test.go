package config

import (
	"encoding/json"
	"testing"
)

func FuzzConfigLoad(f *testing.F) {
	seeds := []string{
		`{"server": {"addr": ":8080"}}`,
		`{}`,
		`{"grpc": {"addr": ":9090", "cert_file": "", "key_file": ""}}`,
		`{"audit": {"path": "/tmp/audit.log", "max_size_mb": 100}}`,
		`{"storage": {"read_timeout_seconds": 30}}`,
		`{"version": "1"}`,
		`{"server": {"addr": ":8080", "read_timeout": 15, "write_timeout": 15}}`,
		`{"log": {"level": "debug", "format": "json"}}`,
		`{"database": {"driver": "postgres", "url": "postgres://localhost/db"}}`,
		`{"modules": {"auth": {"enabled": true}}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// json.Unmarshal into Config must never panic.
		var cfg Config
		_ = json.Unmarshal(data, &cfg)

		// Unmarshal on top of defaults must also never panic.
		def := Default()
		_ = json.Unmarshal(data, def)
	})
}

func FuzzConfigValidate(f *testing.F) {
	seeds := []string{
		`{"server": {"addr": ""}}`,
		`{"server": {"read_timeout": 0}}`,
		`{"server": {"write_timeout": 0}}`,
		`{"log": {"level": "verbose"}}`,
		`{"log": {"format": "xml"}}`,
		`{"server": {"addr": "not-a-valid-addr"}}`,
		`{"grpc": {"addr": "not-a-valid-addr"}}`,
		`{"grpc": {"max_message_size_mb": 0}}`,
		`{"grpc": {"max_message_size_mb": 999}}`,
		`{"server": {"addr": ":8080"}}`,
		`{"server": {"addr": ":8080"}, "log": {"level": "debug", "format": "json"}}`,
		`{"server": {"addr": ":8080", "read_timeout": 15, "write_timeout": 15}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var cfg Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			return
		}

		// validate() must never panic on any Config value.
		_ = cfg.validate()
	})
}
