package installer

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func validateAuthArchive(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := tar.NewReader(f)
	found := false
	for {
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		name := strings.TrimPrefix(header.Name, "./")
		if name == "" && header.Typeflag == tar.TypeDir {
			continue
		}
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || path.Clean(name) != strings.TrimSuffix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return errors.New("unsafe authentication archive path")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return errors.New("authentication archive contains special files")
		}
		if name == "tinyauth.db" {
			found = true
		}
	}
	if !found {
		return errors.New("TinyAuth database is missing from its backup")
	}
	return nil
}

func (r *Real) backupTinyAuth(ctx context.Context, directory string) (result error) {
	stream, ok := r.Runner.(StreamRunner)
	if !ok {
		return errors.New("runner cannot stream authentication data")
	}
	id, err := r.compose(ctx, "ps", "--all", "-q", "tinyauth")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(id)) == "" {
		return errors.New("TinyAuth container is missing; cannot snapshot its database")
	}
	running, err := r.Runner.Run(ctx, nil, "docker", "inspect", "--format", "{{.State.Running}}", strings.TrimSpace(string(id)))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(running)) == "true" {
		if _, err = r.compose(ctx, "stop", "tinyauth"); err != nil {
			return err
		}
		defer func() {
			resume, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, e := r.compose(resume, "start", "tinyauth")
			result = errors.Join(result, e)
		}()
	}
	file := filepath.Join(directory, "tinyauth-data.tar")
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = stream.RunTo(ctx, f, "docker", "run", "--rm", "--network", "none", "--mount", "type=volume,source="+r.tinyAuthVolume()+",target=/data,readonly", "--entrypoint", "tar", tinyAuthImage, "-C", "/data", "-cf", "-", ".")
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return validateAuthArchive(file)
}

func (r *Real) restoreTinyAuth(ctx context.Context, directory string) error {
	file := filepath.Join(directory, "tinyauth-data.tar")
	if err := validateAuthArchive(file); err != nil {
		return err
	}
	input, ok := r.Runner.(StreamInputRunner)
	if !ok {
		return errors.New("runner cannot stream authentication restore")
	}
	if _, err := r.compose(ctx, "stop", "tinyauth"); err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	return input.RunFrom(ctx, f, "docker", "run", "--rm", "-i", "--network", "none", "--mount", "type=volume,source="+r.tinyAuthVolume()+",target=/data", "--entrypoint", "sh", tinyAuthImage, "-c", "rm -f /data/tinyauth.db /data/tinyauth.db-wal /data/tinyauth.db-shm; exec tar -C /data -xf -")
}

func (r *Real) tinyAuthVolume() string {
	if r.state.LayoutVersion >= officialLayout {
		return "remnawave_tinyauth-data"
	}
	return "remnamanager-panel_tinyauth-data"
}
