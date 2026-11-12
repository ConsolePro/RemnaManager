package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"

	"github.com/ConsolePro/RemnaManager/internal/config"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/ConsolePro/RemnaManager/internal/updater"
)

var managerLogName = regexp.MustCompile(`^(install|management)-[0-9]+\.log$`)
var managerTempName = regexp.MustCompile(`^\.manager-(update|candidate)-[0-9]+$`)
var managerConfigTempName = regexp.MustCompile(`^\.(config|update)-[0-9]+$`)

func installedManagerRemoval() error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return services.LocalizedError("Удаление менеджера требует Linux и запуска от root.", "Manager removal requires Linux and root.")
	}
	path, err := os.Executable()
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
	}
	if err != nil || path != updater.ManagerTarget {
		return services.LocalizedError("Удаление доступно для менеджера, установленного через install.sh.", "Removal is available for the manager installed through install.sh.")
	}
	return nil
}

func (r *Real) PrepareManagerRemoval(ctx context.Context) (services.ManagerRemovalPlan, error) {
	if err := installedManagerRemoval(); err != nil {
		return services.ManagerRemovalPlan{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.managerRemovalPlan(ctx, updater.IsManagerExecutable)
}

// The caller closes the TUI, its background tasks and SSH controls first.
func (r *Real) RemoveManager(ctx context.Context, plan services.ManagerRemovalPlan) error {
	if err := installedManagerRemoval(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var locks []*os.File
	defer func() {
		for _, f := range locks {
			_ = f.Close()
		}
	}()
	for _, path := range []string{
		filepath.Join(r.Root, "install.lock"),
		r.systemPath("/usr/local/lib/remnamanager/install.lock"),
		r.systemPath(updater.ManagerTarget + ".update.lock"),
	} {
		if err := noSymlinkParents(path); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		file, err := lockFile(path)
		if err != nil {
			return services.LocalizedError("Выполняется другая установка или обновление. Повторите удаление после её завершения.", "Another installation or update is running. Retry removal after it finishes.")
		}
		locks = append(locks, file)
	}
	return r.removeManagerFiles(ctx, plan, updater.IsManagerExecutable)
}

func (r *Real) managerRemovalPlan(ctx context.Context, inspect func(string) bool) (services.ManagerRemovalPlan, error) {
	var plan services.ManagerRemovalPlan
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	library := r.systemPath(filepath.Dir(updater.ManagerTarget))
	configuration := r.systemPath(filepath.Dir(config.DefaultPath))
	for _, dir := range []string{library, configuration, r.Root} {
		if err := noSymlinkParents(dir); err != nil {
			return plan, err
		}
	}
	// Do not abandon an uncommitted manager replacement.
	if _, err := os.Lstat(r.systemPath(updater.ManagerTarget + ".update.json")); !os.IsNotExist(err) {
		return plan, services.LocalizedError("Сначала завершите восстановление обновления менеджера.", "Finish manager update recovery before removal.")
	}
	target := r.systemPath(updater.ManagerTarget)
	if !inspect(target) {
		return plan, errors.New("installed executable is not RemnaManager")
	}
	for _, dir := range []string{library, configuration, r.Root} {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return plan, err
		}
		for _, entry := range entries {
			path, name := filepath.Join(dir, entry.Name()), entry.Name()
			remove := false
			switch dir {
			case library:
				switch name {
				case "remanager", "remanager.previous", "remna.previous", ".remanager.new":
					if !inspect(path) {
						return plan, fmt.Errorf("refusing to remove an unrecognized executable: %s", path)
					}
					remove = true
				case "remanager.previous.json", "install.lock", "remanager.update.lock":
					remove = true
				}
				remove = remove || managerTempName.MatchString(name)
			case configuration:
				remove = name == "config.json" || name == "update-cache.json" || managerConfigTempName.MatchString(name)
			case r.Root:
				remove = managerLogName.MatchString(name)
			}
			if remove {
				info, err := os.Lstat(path)
				if err != nil {
					return plan, err
				}
				if !info.Mode().IsRegular() {
					return plan, fmt.Errorf("refusing to remove a non-regular manager file: %s", path)
				}
				plan.Remove = append(plan.Remove, path)
			} else if dir != r.Root {
				plan.Preserve = append(plan.Preserve, path)
			}
		}
	}
	// Only aliases which resolve to this exact installed executable belong to us.
	for _, name := range []string{"re", "remanager", "remna_manager", "remna"} {
		path := r.systemPath("/usr/local/bin/" + name)
		if err := noSymlinkParents(filepath.Dir(path)); err != nil {
			return plan, err
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return plan, err
		}
		owned := false
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			owned = err == nil && resolved == target
		} else if info.Mode().IsRegular() && name == "remna" {
			owned = inspect(path)
		}
		if owned {
			plan.Remove = append(plan.Remove, path)
		} else {
			plan.Preserve = append(plan.Preserve, path)
		}
	}
	if _, err := os.Stat(r.Root); err == nil {
		plan.Preserve = append(plan.Preserve, r.Root)
	} else if !os.IsNotExist(err) {
		return plan, err
	}
	plan.Preserve = append(plan.Preserve, r.Stack, "/opt/remnanode")
	sort.Strings(plan.Remove)
	sort.Strings(plan.Preserve)
	hash := sha256.New()
	for _, path := range plan.Remove {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return plan, err
		}
		// Lock files may be created by the remover; open management logs may
		// receive final SSH shutdown messages before the TUI exits.
		if filepath.Base(path) == "install.lock" || filepath.Base(path) == "remanager.update.lock" || filepath.Dir(path) == r.Root && managerLogName.MatchString(filepath.Base(path)) {
			continue
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", path, info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return plan, err
			}
			_, _ = io.WriteString(hash, link)
		} else {
			file, err := os.Open(path)
			if err != nil {
				return plan, err
			}
			_, readErr := io.Copy(hash, file)
			closeErr := file.Close()
			if readErr != nil {
				return plan, readErr
			}
			if closeErr != nil {
				return plan, closeErr
			}
		}
	}
	data, _ := json.Marshal(plan.Preserve)
	_, _ = hash.Write(data)
	plan.Signature = hex.EncodeToString(hash.Sum(nil))
	return plan, nil
}

func (r *Real) removeManagerFiles(ctx context.Context, plan services.ManagerRemovalPlan, inspect func(string) bool) error {
	current, err := r.managerRemovalPlan(ctx, inspect)
	if err != nil {
		return err
	}
	// Never trust paths returned by the UI or a stale confirmation.
	if current.Signature != plan.Signature || plan.Signature == "" {
		return services.LocalizedError("Файлы менеджера изменились. Откройте удаление заново.", "Manager files changed. Open removal again.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Aliases are validated before any unlink. Remove the executable last so a
	// filesystem error leaves a runnable application for another cleanup attempt.
	target := r.systemPath(updater.ManagerTarget)
	for _, path := range current.Remove {
		if path == target {
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	// Empty directories only: custom files and component state are retained.
	for _, path := range []string{r.systemPath(filepath.Dir(config.DefaultPath)), r.systemPath(filepath.Dir(updater.ManagerTarget))} {
		_ = os.Remove(path)
	}
	return nil
}
