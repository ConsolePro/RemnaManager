//go:build linux

package installer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfficialACMERegistrationFallbackAndSharedSAN(t *testing.T) {
	for _, scenario := range []string{"ok", "zero-fails", "zero-missing-url", "all-fail"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			manager := filepath.Join(root, "manager")
			stack := filepath.Join(root, "stack")
			bin := filepath.Join(root, "bin")
			log := filepath.Join(root, "calls")
			os.MkdirAll(filepath.Join(manager, "acme"), 0700)
			os.MkdirAll(bin, 0700)
			acme := `#!/bin/bash
set -eu
printf '%s\n' "$*" >> "$TEST_CALLS"
home=;server=
previous=
for arg in "$@"; do case "$previous" in --home) home=$arg;; --server) server=$arg;; esac; previous=$arg; done
case "$1" in
 --register-account)
  [[ $TEST_SCENARIO != all-fail ]] || exit 1
  [[ $server != zerossl || $TEST_SCENARIO != zero-fails ]] || exit 1
  [[ $server != zerossl || $TEST_SCENARIO != zero-missing-url ]] || exit 0
  path=acme.zerossl.com/v2/DV90
  [[ $server != letsencrypt ]] || path=acme-v02.api.letsencrypt.org/directory
  mkdir -p "$home/ca/$path"
  printf "ACCOUNT_URL='https://example.test/account/1'\n" > "$home/ca/$path/ca.conf"
  ;;
esac
`
			os.WriteFile(filepath.Join(manager, "acme", "acme.sh"), []byte(acme), 0700)
			os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nexit 0\n"), 0700)
			os.WriteFile(filepath.Join(bin, "crontab"), []byte("#!/bin/sh\nprintf '%s/acme/acme.sh --cron\\n' \"$TEST_MANAGER\"\n"), 0700)
			script := filepath.Join(root, "issue.sh")
			os.WriteFile(script, panelACMEScript, 0700)
			command := exec.Command("bash", script, manager, stack, "admin@example.test", "zerossl", "panel.example.test,sub.example.test")
			command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "TEST_CALLS="+log, "TEST_MANAGER="+manager, "TEST_SCENARIO="+scenario)
			output, err := command.CombinedOutput()
			calls, _ := os.ReadFile(log)
			text := string(calls)
			if scenario == "all-fail" {
				if err == nil || strings.Contains(text, "--issue") {
					t.Fatal("issued without an account", err, string(output), text)
				}
				return
			}
			if err != nil {
				t.Fatal(err, string(output))
			}
			if strings.Count(text, "--issue ") != 1 || !strings.Contains(text, "-d panel.example.test -d sub.example.test --alpn --tlsport 8443") {
				t.Fatal("not one SAN issuance", text)
			}
			if !strings.Contains(text, "--key-file "+stack+"/nginx/privkey.key --fullchain-file "+stack+"/nginx/fullchain.pem") {
				t.Fatal("wrong certificate installation paths", text)
			}
			if strings.HasPrefix(scenario, "zero-") && !strings.Contains(text, "--set-default-ca") {
				t.Fatal("missing Lets Encrypt fallback", text)
			}
		})
	}
}
