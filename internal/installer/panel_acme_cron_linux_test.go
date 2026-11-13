//go:build linux

package installer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestACMERenewalCron(t *testing.T) {
	for _, scenario := range []string{"quoted-home", "quoted-command", "unquoted", "missing", "comment", "other-home", "install-fails", "read-fails"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			manager := filepath.Join(root, "manager")
			home := filepath.Join(manager, "acme")
			bin := filepath.Join(root, "bin")
			for _, path := range []string{home, bin} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			entry := "15 2 * * * \"" + home + "\"/acme.sh --cron --home \"" + home + "\" > /dev/null\n"
			unrelated := "0 1 * * * /usr/local/bin/backup\n"
			initial := unrelated
			switch scenario {
			case "quoted-home":
				initial += entry
			case "quoted-command":
				initial += "15 2 * * * \"" + home + "/acme.sh\" --cron --home \"" + home + "\" > /dev/null\n"
			case "unquoted":
				initial += strings.ReplaceAll(entry, "\"", "")
			case "comment":
				initial += "# " + entry
			case "other-home":
				initial += "0 3 * * * /another/acme.sh --cron --home /another > /dev/null\n"
			}
			client := `#!/bin/bash
set -eu
[[ $1 == --install-cronjob ]] || exit 9
printf 'install\n' >> "$TEST_CALLS"
[[ $TEST_SCENARIO != install-fails ]] || exit 4
[[ $TEST_SCENARIO != other-home ]] || exit 0
[[ $TEST_SCENARIO != comment ]] || exit 0
cat "$TEST_ENTRY" >> "$TEST_CRONTAB"
`
			if source := os.Getenv("TEST_ACME_SOURCE"); source != "" && scenario != "install-fails" {
				data, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				client = string(data)
			}
			crontab := `#!/bin/bash
set -eu
if [[ $1 == -l ]]; then
    if [[ $TEST_SCENARIO == read-fails ]]; then echo 'permission denied' >&2; exit 1; fi
    cat "$TEST_CRONTAB"
elif [[ $1 == - ]]; then
    printf 'write\n' >> "$TEST_CALLS"
    cat > "$TEST_CRONTAB"
else exit 5
fi
`
			files := map[string]string{"entry": entry, "crontab": initial, "acme.sh": client,
				"bin/crontab": crontab, "bin/systemctl": "#!/bin/sh\nexit 0\n", "cron.sh": acmeRenewalScript}
			for path, data := range files {
				if path == "acme.sh" {
					path = "manager/acme/acme.sh"
				}
				if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0700); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command("bash", filepath.Join(root, "cron.sh"), manager)
			command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "TEST_SCENARIO="+scenario,
				"TEST_CRONTAB="+filepath.Join(root, "crontab"), "TEST_CALLS="+filepath.Join(root, "calls"), "TEST_ENTRY="+filepath.Join(root, "entry"))
			output, err := command.CombinedOutput()
			fails := scenario == "other-home" || scenario == "comment" || scenario == "install-fails" || scenario == "read-fails"
			if (err != nil) != fails {
				t.Fatalf("err=%v output=%s", err, output)
			}
			after, err := os.ReadFile(filepath.Join(root, "crontab"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(after), initial) {
				t.Fatal("existing jobs changed", string(after))
			}
			calls, _ := os.ReadFile(filepath.Join(root, "calls"))
			if strings.HasPrefix(scenario, "quoted") || scenario == "unquoted" {
				if len(calls) != 0 {
					t.Fatal("existing cron was unnecessarily reinstalled", string(calls))
				}
			} else if !fails && !strings.Contains(string(after), entry) && os.Getenv("TEST_ACME_SOURCE") == "" {
				t.Fatal("missing renewal job", string(after))
			}
			// The same check must be idempotent after a successful installation.
			if !fails {
				repeat := exec.Command("bash", filepath.Join(root, "cron.sh"), manager)
				repeat.Env = command.Env
				if out, err := repeat.CombinedOutput(); err != nil {
					t.Fatalf("repeat: %v %s", err, out)
				}
				final, _ := os.ReadFile(filepath.Join(root, "crontab"))
				if string(final) != string(after) {
					t.Fatal("repeat changed cron")
				}
			}
		})
	}
}
