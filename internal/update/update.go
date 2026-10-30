// Package update implements the self-update of gonetem-server: it pulls the
// gonetem sources from a git repository, downloads a go toolchain, builds
// gonetem and installs it with `make install-<arch>`.
package update

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mroy31/gonetem/internal/options"
)

const (
	STEP_GIT      = "git"
	STEP_GO       = "go"
	STEP_BUILD    = "build"
	STEP_INSTALL  = "install"
	STEP_RESTART  = "restart"
	STEP_COMPLETE = "complete"

	goDownloadURL = "https://go.dev/dl/go%s.%s-%s.tar.gz"

	// default paths used by the makefile install targets.
	defaultInstallDir = "/usr/local/bin"
	serviceName       = "gonetem.service"

	// timeout of the whole update. The build of gonetem and the download of
	// the go toolchain can be slow on small devices.
	updateTimeout = 30 * time.Minute
)

var (
	// ErrDisabled is returned when the update is not enabled in the server
	// configuration file.
	ErrDisabled = errors.New("update is disabled: set update.enabled to true in the server configuration file")

	// ErrBusy is returned when an update is already running.
	ErrBusy = errors.New("an update is already running")

	// ErrUnsupportedPlatform is returned when the server runs on an OS/arch
	// for which no go toolchain or install target exists.
	ErrUnsupportedPlatform = errors.New("update is not supported on this platform")
)

// Logger receives the progress of an update.
type Logger interface {
	// Step is called when a new step starts.
	Step(step string, message string)
	// Log is called for each line of output of the executed commands.
	Log(step string, line string)
}

type Updater struct {
	// Config used to pull, build and install gonetem. It is passed as a
	// parameter (and not read from options.ServerConfig) to keep this package
	// testable.
	Config options.UpdateConfig
	// Logger receives the progress of the update.
	Logger Logger
	// Arch is the value used to select the `make build-<arch>` target.
	// It defaults to runtime.GOARCH.
	Arch string
	// InstallDir is the folder where gonetem binaries are installed. It
	// defaults to /usr/local/bin, as in the makefile.
	InstallDir string
	// Service is the systemd unit restarted at the end of the update. If it
	// is empty, the server is not restarted.
	Service string
}

// NewUpdater returns an Updater using the server configuration.
func NewUpdater(logger Logger) *Updater {
	return &Updater{
		Config:     options.ServerConfig.Update,
		Logger:     logger,
		Arch:       runtime.GOARCH,
		InstallDir: defaultInstallDir,
		Service:    serviceName,
	}
}

// Run pulls, builds and installs gonetem.
func (u *Updater) Run(ctx context.Context) (err error) {
	if !u.Config.Enabled {
		return ErrDisabled
	}
	if err := u.checkPlatform(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	workdir, err := u.prepareWorkdir()
	if err != nil {
		return err
	}
	if u.Config.Workdir == "" {
		// Remove the temporary workdir, but keep it on error to help debugging.
		defer func() {
			if err == nil {
				os.RemoveAll(workdir)
			} else {
				u.log(STEP_GIT, "workdir kept for debugging: "+workdir)
			}
		}()
	}

	if err := u.pull(ctx, workdir); err != nil {
		return err
	}

	gobin, err := u.installGo(ctx, workdir)
	if err != nil {
		return err
	}

	if err := u.build(ctx, workdir, gobin); err != nil {
		return err
	}

	if err := u.install(ctx, workdir); err != nil {
		return err
	}

	// The restart is done last: it stops the process running the update.
	// systemd starts a new process once this one has exited.
	if err := u.restart(ctx); err != nil {
		u.log(STEP_RESTART, err.Error())
		u.step(STEP_COMPLETE, "gonetem has been updated, restart gonetem-server to use the new version")
		return nil
	}

	u.step(STEP_COMPLETE, "gonetem has been updated, gonetem-server is restarting")
	return nil
}

func (u *Updater) step(step, message string) {
	if u.Logger != nil {
		u.Logger.Step(step, message)
	}
}

func (u *Updater) log(step, line string) {
	if u.Logger != nil {
		u.Logger.Log(step, line)
	}
}

// checkPlatform verifies that the update can run on the current platform.
func (u *Updater) checkPlatform() error {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return fmt.Errorf("%w: %s", ErrUnsupportedPlatform, runtime.GOOS)
	}
	if u.Arch == "" {
		return fmt.Errorf("%w: unknown architecture", ErrUnsupportedPlatform)
	}

	// The makefile only defines install targets for these architectures.
	switch u.Arch {
	case "amd64", "arm64":
		return nil
	default:
		return fmt.Errorf("%w: no make install-%s target", ErrUnsupportedPlatform, u.Arch)
	}
}

func (u *Updater) prepareWorkdir() (string, error) {
	if u.Config.Workdir == "" {
		workdir, err := os.MkdirTemp("", "gonetem-update-")
		if err != nil {
			return "", fmt.Errorf("unable to create temporary directory: %w", err)
		}
		return workdir, nil
	}

	if err := os.MkdirAll(u.Config.Workdir, 0755); err != nil {
		return "", fmt.Errorf("unable to create update directory %s: %w", u.Config.Workdir, err)
	}
	return u.Config.Workdir, nil
}

// run executes a command and sends its output to the logger.
func (u *Updater) run(ctx context.Context, step, dir string, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("unable to run %s: %w", name, err)
	}

	// keep the last lines to explain failures to the user.
	var lastLines []string
	var mu sync.Mutex

	done := make(chan struct{}, 2)
	scan := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			u.log(step, line)

			mu.Lock()
			lastLines = append(lastLines, line)
			if len(lastLines) > 5 {
				lastLines = lastLines[1:]
			}
			mu.Unlock()
		}
		done <- struct{}{}
	}
	go scan(stdout)
	go scan(stderr)
	<-done
	<-done

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s was interrupted: %w", name, ctx.Err())
		}
		mu.Lock()
		details := strings.Join(lastLines, "\n")
		mu.Unlock()
		if details != "" {
			return fmt.Errorf("%s failed: %w\n%s", name, err, details)
		}
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

// pull clones the gonetem sources and checks out the configured branch.
func (u *Updater) pull(ctx context.Context, workdir string) error {
	u.step(STEP_GIT, fmt.Sprintf("Pull gonetem sources from %s (branch %s)", u.Config.Repository, u.Config.Branch))

	src := filepath.Join(workdir, "src")
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("unable to clean %s: %w", src, err)
	}

	if err := u.run(ctx, STEP_GIT, workdir, nil, "git", "clone", "--depth", "1", "--branch", u.Config.Branch, u.Config.Repository, src); err != nil {
		return err
	}

	u.log(STEP_GIT, "HEAD: "+u.gitHead(ctx, src))
	return nil
}

func (u *Updater) gitHead(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "log", "-1", "--format=%h %s").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// installGo downloads the configured version of go and returns the path of
// the go binary.
func (u *Updater) installGo(ctx context.Context, workdir string) (string, error) {
	version := strings.TrimPrefix(u.Config.GoVersion, "go")
	if version == "" {
		return "", errors.New("update.goVersion is not configured")
	}

	gobin := filepath.Join(workdir, "go", "bin", "go")
	if out, err := exec.Command(gobin, "version").Output(); err == nil && strings.Contains(string(out), "go"+version) {
		u.step(STEP_GO, fmt.Sprintf("Go %s already available in %s", version, workdir))
		return gobin, nil
	}

	url := fmt.Sprintf(goDownloadURL, version, runtime.GOOS, goArch(u.Arch))
	u.step(STEP_GO, fmt.Sprintf("Download go %s from %s", version, url))

	archive := filepath.Join(workdir, "go.tar.gz")
	if err := download(ctx, url, archive); err != nil {
		return "", err
	}

	if err := os.RemoveAll(filepath.Join(workdir, "go")); err != nil {
		return "", fmt.Errorf("unable to clean go directory: %w", err)
	}
	if err := extractTarGz(archive, workdir); err != nil {
		return "", fmt.Errorf("unable to extract %s: %w", archive, err)
	}
	os.Remove(archive)

	u.step(STEP_GO, fmt.Sprintf("Go %s installed in %s", version, workdir))
	return gobin, nil
}

// goArch returns the architecture name used by go download urls.
func goArch(arch string) string {
	switch arch {
	case "armv7":
		return "armv6l"
	default:
		return arch
	}
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("unable to download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to download %s: unexpected status %s", url, resp.Status)
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("unable to create %s: %w", dest, err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("unable to download %s: %w", url, err)
	}
	return f.Close()
}

func extractTarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		target, err := safeJoin(dest, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			_ = os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		}
	}
}

// safeJoin joins name to dir and refuses paths escaping dir.
func safeJoin(dir, name string) (string, error) {
	target := filepath.Join(dir, filepath.FromSlash(name))
	if target != dir && !strings.HasPrefix(target, dir+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid archive path %q", name)
	}
	return target, nil
}

// goHome returns the folder used for the go caches. It is not placed in the
// temporary workdir so that the module cache is reused by the next updates.
func (u *Updater) goHome() string {
	if u.Config.Workdir != "" {
		return filepath.Join(u.Config.Workdir, "go-home")
	}
	return filepath.Join(os.TempDir(), "gonetem-update-go")
}

// goEnv returns the environment used to run the go toolchain.
//
// gonetem-server is launched by systemd, which does not define HOME: without
// it, go cannot locate its build cache and its module cache.
func (u *Updater) goEnv(gobin string) ([]string, error) {
	gohome := u.goHome()
	if err := os.MkdirAll(filepath.Join(gohome, "pkg", "mod"), 0755); err != nil {
		return nil, fmt.Errorf("unable to create go module cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(gohome, "build-cache"), 0755); err != nil {
		return nil, fmt.Errorf("unable to create go build cache: %w", err)
	}

	// The makefile calls `go` from the PATH: put the downloaded toolchain
	// first. GOTOOLCHAIN=local forbids go from downloading another toolchain.
	return []string{
		"PATH=" + filepath.Dir(gobin) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + gohome,
		"GOPATH=" + gohome,
		"GOMODCACHE=" + filepath.Join(gohome, "pkg", "mod"),
		"GOCACHE=" + filepath.Join(gohome, "build-cache"),
		"GOTOOLCHAIN=local",
	}, nil
}

// build compiles gonetem with the downloaded go toolchain.
func (u *Updater) build(ctx context.Context, workdir, gobin string) error {
	u.step(STEP_BUILD, fmt.Sprintf("Build gonetem for %s", u.Arch))

	env, err := u.goEnv(gobin)
	if err != nil {
		return err
	}

	src := filepath.Join(workdir, "src")
	if err := u.run(ctx, STEP_BUILD, src, env, "make", "build-"+u.Arch); err != nil {
		return err
	}

	u.step(STEP_BUILD, "gonetem has been built")
	return nil
}

// install copies the built binaries in the install directory.
//
// The makefile `install-<arch>` target is not used: it stops gonetem.service,
// which kills the server running the update. The service is restarted later.
func (u *Updater) install(ctx context.Context, workdir string) error {
	installDir := u.InstallDir
	if installDir == "" {
		installDir = defaultInstallDir
	}
	u.step(STEP_INSTALL, fmt.Sprintf("Install gonetem in %s", installDir))

	if err := os.MkdirAll(installDir, 0755); err != nil {
		return fmt.Errorf("unable to create %s: %w", installDir, err)
	}

	binDir := filepath.Join(workdir, "src", "bin")
	for _, binary := range []string{"gonetem-server", "gonetem-console"} {
		src := filepath.Join(binDir, fmt.Sprintf("%s_%s", binary, u.Arch))
		dest := filepath.Join(installDir, binary)

		if err := copyFile(src, dest); err != nil {
			return err
		}
		u.log(STEP_INSTALL, fmt.Sprintf("%s -> %s", src, dest))
	}

	u.step(STEP_INSTALL, "gonetem has been installed")
	return nil
}

// copyFile copies src to dest, replacing dest atomically so the running
// binary is never left truncated.
func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("unable to open %s: %w", src, err)
	}
	defer in.Close()

	// write in the same directory to keep the rename atomic.
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".gonetem-update-")
	if err != nil {
		return fmt.Errorf("unable to create temporary file in %s: %w", filepath.Dir(dest), err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("unable to copy %s: %w", src, err)
	}
	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		return fmt.Errorf("unable to set permissions on %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("unable to install %s: %w", dest, err)
	}
	return nil
}

// restart asks systemd to restart gonetem-server. The restart is scheduled
// without blocking: this process is stopped by systemd.
func (u *Updater) restart(ctx context.Context) error {
	if u.Service == "" {
		return errors.New("no systemd service configured, restart gonetem-server manually")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("systemctl not found, restart gonetem-server manually")
	}

	u.step(STEP_RESTART, "Restart "+u.Service)
	if err := exec.CommandContext(ctx, "systemctl", "restart", "--no-block", u.Service).Run(); err != nil {
		return fmt.Errorf("unable to restart %s: %w", u.Service, err)
	}
	return nil
}
