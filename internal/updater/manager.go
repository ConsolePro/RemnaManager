package updater

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const ManagerTarget = "/usr/local/lib/remnamanager/remanager"
const maxManagerSize = 100 << 20

// IsManagerExecutable inspects build metadata without executing the file.
func IsManagerExecutable(path string) bool {
	info, err := buildinfo.ReadFile(path)
	return err == nil && info.Main.Path == "github.com/ConsolePro/RemnaManager" && info.Path == "github.com/ConsolePro/RemnaManager/cmd/remna"
}

type Plan struct {
	Version, Current, SHA256, CurrentSHA256, URL string
	Rollback                                     bool
}
type savedBinary struct{ Version, SHA256 string }
type updateJournal struct {
	Previous savedBinary
	Next     savedBinary
}
type Manager struct {
	Catalog      Catalog
	Target, Arch string
	// Validate is injectable for filesystem tests; production uses ELF, Go build
	// identity and a version command before and after replacing the executable.
	Validate    func(context.Context, string, string) error
	ReadVersion func(context.Context, string) (string, error)
}

func NewManager() (*Manager, error) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return nil, errors.New("manager updates require Linux and root")
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	if executable != ManagerTarget {
		return nil, errors.New("self-update is available for the installed /usr/local/lib/remnamanager/remanager executable")
	}
	return &Manager{Catalog: Catalog{Repo: "ConsolePro/RemnaManager"}, Target: executable, Arch: runtime.GOARCH}, nil
}
func binaryHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func (m *Manager) version(ctx context.Context, path string) (string, error) {
	if m.ReadVersion != nil {
		return m.ReadVersion(ctx, path)
	}
	data, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 || !TagPattern.MatchString(fields[0]) {
		return "", errors.New("invalid manager version output")
	}
	return fields[0], nil
}
func (m *Manager) validate(ctx context.Context, path, version string) error {
	if m.Validate != nil {
		return m.Validate(ctx, path, version)
	}
	file, err := elf.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	expected := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[m.Arch]
	if expected == 0 || file.Machine != expected || file.Class != elf.ELFCLASS64 {
		return errors.New("manager executable architecture mismatch")
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return err
	}
	if info.Main.Path != "github.com/ConsolePro/RemnaManager" {
		return errors.New("release is not a RemnaManager Go executable")
	}
	got, err := m.version(ctx, path)
	if err != nil {
		return err
	}
	if got != version {
		return fmt.Errorf("manager version mismatch: expected %s, got %s", version, got)
	}
	return nil
}
func privatePath(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("update paths must not contain symbolic links")
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}
func atomicFile(path string, data []byte, mode os.FileMode) error {
	if err := privatePath(path); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".manager-update-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		directory, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		defer directory.Close()
		return directory.Sync()
	}
	return nil
}
func readBinary(path string) ([]byte, error) {
	if err := privatePath(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxManagerSize {
		return nil, errors.New("invalid manager executable file")
	}
	return os.ReadFile(path)
}
func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicFile(path, data, 0600)
}
func (m *Manager) recover(ctx context.Context) error {
	path := m.Target + ".update.json"
	if err := privatePath(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal updateJournal
	if json.Unmarshal(data, &journal) != nil || !SHA256Pattern.MatchString(journal.Previous.SHA256) || !SHA256Pattern.MatchString(journal.Next.SHA256) {
		return errors.New("invalid manager recovery journal")
	}
	current, err := readBinary(m.Target)
	if err != nil {
		return err
	}
	switch binaryHash(current) {
	case journal.Previous.SHA256:
	case journal.Next.SHA256:
		if err := m.validate(ctx, m.Target, journal.Next.Version); err != nil {
			previous, e := readBinary(m.Target + ".previous")
			if e != nil {
				return e
			}
			if binaryHash(previous) != journal.Previous.SHA256 {
				return errors.New("manager backup checksum mismatch")
			}
			if e = m.validate(ctx, m.Target+".previous", journal.Previous.Version); e != nil {
				return e
			}
			if e = atomicFile(m.Target, previous, 0755); e != nil {
				return e
			}
		}
	default:
		return errors.New("installed manager changed externally; recovery refused")
	}
	return os.Remove(path)
}
func (m *Manager) Prepare(ctx context.Context, tag string, rollback bool) (Plan, error) {
	unlock, err := managerLock(m.Target)
	if err != nil {
		return Plan{}, err
	}
	defer unlock()
	if err = m.recover(ctx); err != nil {
		return Plan{}, err
	}
	current, err := readBinary(m.Target)
	if err != nil {
		return Plan{}, err
	}
	version, err := m.version(ctx, m.Target)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Current: version, CurrentSHA256: binaryHash(current), Rollback: rollback}
	if rollback {
		if err := privatePath(m.Target + ".previous.json"); err != nil {
			return plan, err
		}
		data, e := os.ReadFile(m.Target + ".previous.json")
		if e != nil {
			return plan, e
		}
		var saved savedBinary
		if json.Unmarshal(data, &saved) != nil || !TagPattern.MatchString(saved.Version) || !SHA256Pattern.MatchString(saved.SHA256) {
			return plan, errors.New("invalid previous manager metadata")
		}
		plan.Version, plan.SHA256 = saved.Version, saved.SHA256
		return plan, nil
	}
	release, err := m.Catalog.Get(ctx, tag)
	if err != nil {
		return plan, err
	}
	if (tag == "" || tag == "latest") && Newer(version, release.Version) {
		return plan, errors.New("latest stable release is older than the installed manager; select a version explicitly to downgrade")
	}
	asset, err := m.Catalog.Asset(release, "remanager-linux-"+m.Arch)
	if err != nil {
		return plan, err
	}
	if asset.Size <= 0 || asset.Size > maxManagerSize {
		return plan, errors.New("invalid manager asset size")
	}
	digest, err := m.Catalog.Digest(ctx, release, asset)
	if err != nil {
		return plan, err
	}
	plan.Version, plan.SHA256, plan.URL = release.Version, digest, asset.URL
	return plan, nil
}
func (m *Manager) Apply(ctx context.Context, plan Plan) error {
	unlock, err := managerLock(m.Target)
	if err != nil {
		return err
	}
	defer unlock()
	if err = m.recover(ctx); err != nil {
		return err
	}
	if !TagPattern.MatchString(plan.Version) || !SHA256Pattern.MatchString(plan.SHA256) {
		return errors.New("invalid manager update plan")
	}
	current, err := readBinary(m.Target)
	if err != nil {
		return err
	}
	if binaryHash(current) != plan.CurrentSHA256 {
		return errors.New("installed manager changed since confirmation")
	}
	var candidate []byte
	if plan.Rollback {
		candidate, err = readBinary(m.Target + ".previous")
	} else {
		release, e := m.Catalog.Get(ctx, plan.Version)
		if e != nil {
			return e
		}
		asset, e := m.Catalog.Asset(release, "remanager-linux-"+m.Arch)
		if e != nil {
			return e
		}
		digest, e := m.Catalog.Digest(ctx, release, asset)
		if e != nil {
			return e
		}
		if asset.URL != plan.URL || digest != plan.SHA256 {
			return errors.New("release changed since confirmation")
		}
		candidate, err = m.Catalog.Read(ctx, asset.URL, maxManagerSize)
	}
	if err != nil {
		return err
	}
	if binaryHash(candidate) != plan.SHA256 {
		return errors.New("manager download SHA256 mismatch")
	}
	file, err := os.CreateTemp(filepath.Dir(m.Target), ".manager-candidate-*")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	if err = file.Chmod(0755); err == nil {
		_, err = file.Write(candidate)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = m.validate(ctx, path, plan.Version); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = atomicFile(m.Target+".previous", current, 0755); err != nil {
		return err
	}
	previous := savedBinary{Version: plan.Current, SHA256: plan.CurrentSHA256}
	if err = writeJSON(m.Target+".previous.json", previous); err != nil {
		return err
	}
	if err = writeJSON(m.Target+".update.json", updateJournal{Previous: previous, Next: savedBinary{Version: plan.Version, SHA256: plan.SHA256}}); err != nil {
		return err
	}
	if err = atomicFile(m.Target, candidate, 0755); err != nil {
		return err
	}
	if err = m.validate(ctx, m.Target, plan.Version); err != nil {
		// Recovery must survive cancellation of the interactive operation.
		if restoreErr := atomicFile(m.Target, current, 0755); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		_ = os.Remove(m.Target + ".update.json")
		return fmt.Errorf("new manager failed startup; previous executable restored: %w", err)
	}
	return os.Remove(m.Target + ".update.json")
}
