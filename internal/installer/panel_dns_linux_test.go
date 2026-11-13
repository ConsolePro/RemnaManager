//go:build linux

package installer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWildcardACMEEncryptedRenewalAndIssueFailure(t *testing.T) {
	for _, provider := range []string{"cloudflare", "gcore", "bunny"} {
		for _, fail := range []bool{false, true} {
			t.Run(provider+map[bool]string{false: "/success", true: "/failure"}[fail], func(t *testing.T) {
				root := t.TempDir()
				manager := filepath.Join(root, "manager")
				stack := filepath.Join(root, "stack")
				bin := filepath.Join(root, "bin")
				for _, dir := range []string{bin, filepath.Join(manager, "acme", "dnsapi"), filepath.Join(manager, "acme", "ca", "acme-v02.api.letsencrypt.org", "directory")} {
					os.MkdirAll(dir, 0700)
				}
				os.WriteFile(filepath.Join(manager, "acme", "ca", "acme-v02.api.letsencrypt.org", "directory", "ca.conf"), []byte("ACCOUNT_URL='https://fixture/account'\n"), 0600)
				hook, name := "dns_cf", "CF_Token"
				if provider == "gcore" {
					hook, name = "dns_gcore", "GCORE_Key"
				}
				if provider == "bunny" {
					hook, name = "dns_bunny", "BUNNY_API_KEY"
				}
				os.WriteFile(filepath.Join(manager, "acme", "dnsapi", hook+".sh"), []byte("  _saveaccountconf_mutable "+name+" \"$"+name+"\"\n"), 0600)
				client := `#!/bin/bash
set -eu
printf '%s\n' "$@" >> "$TEST_CALLS"
case "$1" in
 --register-account) exit 0;;
 --issue)
  [[ ${!TEST_TOKEN_NAME} == "$TEST_TOKEN" ]]
  [[ $TEST_FAIL != yes ]] || exit 73
  ;;
 --cron) [[ ${!TEST_TOKEN_NAME} == "$TEST_TOKEN" ]]; printf renewed >> "$TEST_RENEWED";;
esac
`
				os.WriteFile(filepath.Join(manager, "acme", "acme.sh"), []byte(client), 0700)
				os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nexit 0\n"), 0700)
				os.WriteFile(filepath.Join(bin, "crontab"), []byte("#!/bin/sh\nif [ \"$1\" = -l ]; then cat \"$TEST_CRONTAB\"; else cat > \"$TEST_CRONTAB\"; fi\n"), 0700)
				cron := filepath.Join(root, "crontab")
				os.WriteFile(cron, []byte("0 1 * * * /usr/local/bin/backup\n"), 0600)
				path := filepath.Join(root, "issue.sh")
				os.WriteFile(path, panelACMEScript, 0700)
				token := "fixture-secret-dns-api-key"
				env := append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RM_CERT_METHOD="+provider, "RM_DNS_TOKEN="+token, "TEST_TOKEN_NAME="+name, "TEST_TOKEN="+token, "TEST_CALLS="+filepath.Join(root, "calls"), "TEST_RENEWED="+filepath.Join(root, "renewed"), "TEST_CRONTAB="+cron)
				if fail {
					env = append(env, "TEST_FAIL=yes")
				} else {
					env = append(env, "TEST_FAIL=no")
				}
				cmd := exec.Command("bash", path, manager, stack, "admin@example.test", "letsencrypt", "panel.example.com,sub.other.test")
				cmd.Env = env
				output, err := cmd.CombinedOutput()
				calls, _ := os.ReadFile(filepath.Join(root, "calls"))
				if fail {
					if err == nil || strings.Contains(string(calls), "--install-cert") {
						t.Fatal("failed issuance accepted", err, string(output))
					}
					return
				}
				if err != nil {
					t.Fatal(err, string(output))
				}
				for _, arg := range []string{"--dns\n" + hook, "*.panel.example.com", "*.sub.other.test", "panel.example.com", "sub.other.test"} {
					if !strings.Contains(string(calls), arg) {
						t.Fatal("missing ACME argument", arg, string(calls))
					}
				}
				for _, file := range []string{"dns-token.enc", "acme/dnsapi/" + hook + ".sh", "renew-dns.sh"} {
					data, _ := os.ReadFile(filepath.Join(manager, file))
					if strings.Contains(string(data), token) {
						t.Fatal("plaintext credential on disk", file)
					}
				}
				cmd = exec.Command("bash", filepath.Join(manager, "renew-dns.sh"))
				cmd.Env = env
				if output, err = cmd.CombinedOutput(); err != nil {
					t.Fatal(err, string(output))
				}
				if _, err = os.Stat(filepath.Join(root, "renewed")); err != nil {
					t.Fatal("renewal never ran", err)
				}
				data, _ := os.ReadFile(cron)
				if strings.Count(string(data), "# RemnaManager DNS") != 1 || !strings.Contains(string(data), "/usr/local/bin/backup") {
					t.Fatal("renewal cron conflict", string(data))
				}
			})
		}
	}
}
