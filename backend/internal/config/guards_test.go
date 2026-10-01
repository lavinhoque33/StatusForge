package config

import (
	"strings"
	"testing"
)

func TestConfigGuards(t *testing.T) {
	base := map[string]string{"STATUSFORGE_DYNAMODB_ENDPOINT": "http://127.0.0.1:8000"}
	for _, tc := range []struct{ key, value string }{{"STATUSFORGE_ALLOWED_TARGETS", ""}, {"STATUSFORGE_ALLOWED_TARGETS", "example.com:80"}, {"STATUSFORGE_ALLOWED_TARGETS", "127.0.0.1:0"}, {"STATUSFORGE_ALLOWED_TARGETS", "127.0.0.1:65536"}, {"STATUSFORGE_DYNAMODB_TABLE", "ab"}, {"STATUSFORGE_DYNAMODB_TABLE", "abc/def"}} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			vars := map[string]string{}
			for k, v := range base {
				vars[k] = v
			}
			vars[tc.key] = tc.value
			_, err := Load(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("expected %s error, got %v", tc.key, err)
			}
		})
	}
	cfg, err := Load(func(k string) (string, bool) { v, ok := base[k]; return v, ok })
	if err != nil || cfg.AllowedTargets != "127.0.0.1:8090" || cfg.DynamoDBTable != "statusforge" {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
}
