//go:build linux

package installer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestACMEFirstInstallationBootstrap(t *testing.T) {
	for name, script := range map[string][]byte{"official": panelACMEScript, "legacy": legacyPanelACMEScript} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			manager := filepath.Join(root, "manager with spaces")
			bin := filepath.Join(root, "bin")
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			// Match get.acme.sh's first-argument conversion and forwarding contract.
			bootstrap := `#!/bin/sh
set -eu
_email="$1"
if [ "$_email" ]; then
    shift
    _email="--$(echo "$_email" | tr '=' ' ')"
fi
curl -L https://raw.githubusercontent.com/acmesh-official/acme.sh/master/acme.sh | sh -s -- --install-online $_email "$@"
`
			client := `#!/bin/sh
set -eu
printf '%s\n' "$@" >> "$TEST_CALLS"
if [ "$1" = --install-online ]; then
    shift
    [ "$1" = --email ] && [ "$2" = admin@example.test ] || exit 31
    shift 2
    [ "$1" = --home ] && [ "$2" = "$TEST_MANAGER/acme" ] || exit 32
    shift 2
    [ "$1" = --config-home ] && [ "$2" = "$TEST_MANAGER/acme" ] || exit 33
    [ "$#" = 2 ] || exit 34
    mkdir -p "$TEST_MANAGER/acme"
    cp "$TEST_CLIENT_SOURCE" "$TEST_MANAGER/acme/acme.sh"
    chmod 700 "$TEST_MANAGER/acme/acme.sh"
    exit 0
fi
# Deliberately stop at registration: no external account or issuance in this test.
exit 42
`
			curl := `#!/bin/sh
set -eu
if [ "$1" = -L ]; then
    cat "$TEST_CLIENT_SOURCE"
else
    while [ "$1" != -o ]; do shift; done
    cp "$TEST_BOOTSTRAP" "$2"
fi
`
			files := map[string]string{"bootstrap.sh": bootstrap, "client.sh": client, "bin/curl": curl, "issue.sh": string(script)}
			for path, content := range files {
				if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0700); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command("bash", filepath.Join(root, "issue.sh"), manager, filepath.Join(root, "stack"), "admin@example.test", "zerossl", "panel.example.test,sub.example.test")
			command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "TEST_MANAGER="+manager,
				"TEST_BOOTSTRAP="+filepath.Join(root, "bootstrap.sh"), "TEST_CLIENT_SOURCE="+filepath.Join(root, "client.sh"), "TEST_CALLS="+filepath.Join(root, "calls"))
			output, err := command.CombinedOutput()
			calls, readErr := os.ReadFile(filepath.Join(root, "calls"))
			if err == nil || readErr != nil || !strings.Contains(string(calls), "--register-account\n") || strings.Contains(string(calls), "--issue\n") {
				t.Fatalf("bootstrap did not reach registration safely: err=%v, calls=%s, output=%s", err, calls, output)
			}
			if _, err := os.Stat(filepath.Join(manager, "acme", "acme.sh")); err != nil {
				t.Fatal("client was not installed in the configured home:", err)
			}
		})
	}
}

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
if [[ $1 == --issue && -n ${TEST_ACME_SOURCE:-} ]]; then
    exec bash "$TEST_ACME_PARSER" "$@"
fi
`
			os.WriteFile(filepath.Join(manager, "acme", "acme.sh"), []byte(acme), 0700)
			os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nexit 0\n"), 0700)
			os.WriteFile(filepath.Join(bin, "crontab"), []byte("#!/bin/sh\nprintf '%s/acme/acme.sh --cron\\n' \"$TEST_MANAGER\"\n"), 0700)
			// When supplied, exercise the real upstream command-line parser, while
			// replacing only issuance so the test makes no network requests.
			parser := filepath.Join(root, "parse.sh")
			os.WriteFile(parser, []byte(`#!/bin/bash
source <(sed '$d' "$TEST_ACME_SOURCE")
issue() {
    [[ $1 == alpn && $2 == panel.example.test && $3 == sub.example.test && $Le_TLSPort == 8443 ]] || {
        printf 'Wrong challenge selection: root=%s domain=%s SAN=%s TLS-port=%s\n' "$1" "$2" "$3" "$Le_TLSPort" >&2
        return 1
    }
}
_process "$@"
`), 0700)
			script := filepath.Join(root, "issue.sh")
			os.WriteFile(script, panelACMEScript, 0700)
			command := exec.Command("bash", script, manager, stack, "admin@example.test", "zerossl", "panel.example.test,sub.example.test")
			command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "TEST_CALLS="+log, "TEST_MANAGER="+manager, "TEST_SCENARIO="+scenario, "TEST_ACME_PARSER="+parser)
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
			if scenario == "ok" && os.Getenv("TEST_ACME_SOURCE") != "" {
				bad := exec.Command("bash", parser, "--issue", "--home", filepath.Join(manager, "acme"), "--standalone", "-d", "panel.example.test", "-d", "sub.example.test", "--alpn", "--tlsport", "8443")
				bad.Env = command.Env
				badOutput, badErr := bad.CombinedOutput()
				if badErr == nil || !strings.Contains(string(badOutput), "root=no,alpn") {
					t.Fatalf("upstream parser did not reproduce the original HTTP/ALPN conflict: %v %s", badErr, badOutput)
				}
			}
			if strings.Count(text, "--issue ") != 1 || !strings.Contains(text, "-d panel.example.test -d sub.example.test --alpn --tlsport 8443") {
				t.Fatal("not one SAN issuance", text)
			}
			if strings.Contains(text, "--standalone") || strings.Contains(text, "--webroot") {
				t.Fatal("HTTP-01 conflicts with ALPN challenge selection", text)
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
