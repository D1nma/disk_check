package annotate

import "strings"

// Shared annotations of the system tables.
var (
	annJournal   = Annotation{Label: "journal systemd", Cleanup: "journalctl --vacuum-size=500M"}
	annCoredumps = Annotation{Label: "core dumps", Cleanup: "coredumpctl list ; supprimer les anciens dumps"}
	annCoredump  = Annotation{Label: "core dump", Cleanup: "coredumpctl list ; supprimer les anciens dumps"}
	annTemp      = Annotation{Label: "fichiers temporaires", Cleanup: "systemd-tmpfiles --clean"}
	annRotated   = Annotation{Label: "log archivé (rotation)", Cleanup: "vérifier logrotate ; supprimable si plus utile"}
)

// systemDirs annotates well-known directories by exact absolute path.
// To add a rule, add a line.
var systemDirs = map[string]Annotation{
	// Logs
	"/var/log/journal": annJournal,
	"/run/log/journal": annJournal,
	"/var/log/audit":   {Label: "logs auditd", Cleanup: "réduire max_log_file / num_logs (auditd.conf)"},

	// Package manager caches
	"/var/cache/apt/archives": {Label: "cache de paquets apt", Cleanup: "apt-get clean", Regen: true},
	"/var/cache/apt":          {Label: "cache apt", Cleanup: "apt-get clean", Regen: true},
	"/var/lib/apt/lists":      {Label: "index des dépôts apt", Cleanup: "régénéré par apt-get update", Regen: true},
	"/var/cache/dnf":          {Label: "cache de paquets dnf", Cleanup: "dnf clean all", Regen: true},
	"/var/cache/libdnf5":      {Label: "cache de paquets dnf5", Cleanup: "dnf clean all", Regen: true},
	"/var/cache/yum":          {Label: "cache de paquets yum", Cleanup: "yum clean all", Regen: true},
	"/var/cache/zypp":         {Label: "cache de paquets zypper", Cleanup: "zypper clean", Regen: true},
	"/var/cache/pacman/pkg":   {Label: "cache de paquets pacman", Cleanup: "paccache -r", Regen: true},

	// Crash dumps
	"/var/lib/systemd/coredump": annCoredumps,
	"/var/crash":                annCoredumps,

	// Application stores
	"/var/lib/snapd/snaps": {Label: "paquets snap (révisions)", Cleanup: "snap set system refresh.retain=2"},
	"/nix/store":           {Label: "store Nix", Cleanup: "nix-collect-garbage -d"},
	"/var/lib/flatpak":     {Label: "flatpak (applications et runtimes)", Cleanup: "flatpak uninstall --unused"},

	// Temporary files
	"/tmp":     annTemp,
	"/var/tmp": annTemp,
}

// systemChildren annotates every direct child (file or directory) of a
// well-known directory.
var systemChildren = map[string]Annotation{
	"/var/log/journal":          annJournal, // one directory per machine ID
	"/run/log/journal":          annJournal,
	"/var/lib/systemd/coredump": annCoredump,
	"/var/crash":                annCoredump,
}

func (a *Annotator) matchSystem(q *query) (Annotation, bool) {
	if q.isDir {
		if ann, ok := systemDirs[q.path]; ok {
			return ann, true
		}
	}
	if ann, ok := systemChildren[q.dir]; ok {
		return ann, true
	}
	return Annotation{}, false
}

// rotatedLogExts are the suffixes of compressed or renamed rotated logs.
var rotatedLogExts = [...]string{".gz", ".xz", ".zst", ".bz2", ".old"}

// matchRotatedLog labels a rotated log: a file under /var/log named
// *.gz, *.xz, *.zst, *.bz2, *.old, *.<digits> (syslog.1, app.log.3) or with a
// logrotate date suffix (messages-20240107).
func (a *Annotator) matchRotatedLog(q *query) (Annotation, bool) {
	if q.isDir || !strings.HasPrefix(q.path, "/var/log/") || !isRotatedLogName(q.name) {
		return Annotation{}, false
	}
	return annRotated, true
}

func isRotatedLogName(name string) bool {
	for _, ext := range rotatedLogExts {
		if len(name) > len(ext) && strings.HasSuffix(name, ext) {
			return true
		}
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 && isDigits(name[i+1:]) {
		return true
	}
	// logrotate "dateext": -YYYYMMDD, or -YYYYMMDDHH with an hourly dateformat.
	if i := strings.LastIndexByte(name, '-'); i > 0 {
		d := name[i+1:]
		if (len(d) == 8 || len(d) == 10) && isDigits(d) && (d[:2] == "19" || d[:2] == "20") {
			return true
		}
	}
	return false
}

// homeDirs annotates well-known directories by path relative to a home
// directory (/root, /home/<u>, /Users/<u>, /var/home/<u>).
var homeDirs = map[string]Annotation{
	".cache":          {Label: "cache utilisateur", Regen: true},
	".cache/go-build": {Label: "cache de build Go", Cleanup: "go clean -cache", Regen: true},
	".cache/pip":      {Label: "cache pip", Cleanup: "pip cache purge", Regen: true},
	".npm/_cacache":   {Label: "cache npm", Cleanup: "npm cache clean --force", Regen: true},
	".gradle/caches":  {Label: "cache Gradle", Regen: true},
	".m2/repository":  {Label: "dépôt Maven local", Regen: true},
	"go/pkg/mod":      {Label: "cache de modules Go", Cleanup: "go clean -modcache", Regen: true},
	".cargo/registry": {Label: "registre Cargo (cache)", Regen: true},
	"Library/Caches":  {Label: "cache utilisateur (macOS)", Regen: true},

	".local/share/Trash":   {Label: "corbeille", Cleanup: "vider la corbeille"},
	".local/share/flatpak": {Label: "flatpak utilisateur", Cleanup: "flatpak uninstall --user --unused"},
}

func (a *Annotator) matchHome(q *query) (Annotation, bool) {
	if !q.isDir {
		return Annotation{}, false
	}
	if _, rel, ok := splitHome(q.path); ok && rel != "" {
		if ann, ok := homeDirs[rel]; ok {
			return ann, true
		}
	}
	return Annotation{}, false
}

// devArtifact is a build or dependency directory recognised by its name
// anywhere on disk, optionally confirmed by a marker file.
type devArtifact struct {
	name   string // directory name
	marker string // file that must exist, "" for none
	inside bool   // marker is inside the directory (else beside it, in the parent)
	ann    Annotation
}

// devArtifacts is checked last: these names are matched anywhere. The marker
// keeps out look-alikes, e.g. /usr/lib/node_modules (global npm packages,
// no package.json beside it) or a "target" directory that is not a Cargo
// build.
var devArtifacts = [...]devArtifact{
	{name: "node_modules", marker: "package.json", ann: Annotation{Label: "dépendances npm", Cleanup: "npm ci pour reconstruire", Regen: true}},
	{name: "target", marker: "Cargo.toml", ann: Annotation{Label: "build Rust", Cleanup: "cargo clean", Regen: true}},
	{name: "__pycache__", ann: Annotation{Label: "cache bytecode Python", Regen: true}},
	{name: ".venv", marker: "pyvenv.cfg", inside: true, ann: Annotation{Label: "virtualenv Python", Regen: true}},
	{name: "venv", marker: "pyvenv.cfg", inside: true, ann: Annotation{Label: "virtualenv Python", Regen: true}},
}

// managedRoots hold files owned by a container engine or package store:
// deleting a node_modules or __pycache__ in there would corrupt an image
// layer or a running pod, so developer-artifact advice is suppressed.
var managedRoots = [...]string{
	podmanRoot,
	"/var/lib/containerd",
	"/var/lib/rancher",
	kubeletPods,
	"/nix/store",
	"/var/lib/flatpak",
	"/var/lib/snapd",
}

func (a *Annotator) inManagedStorage(p string) bool {
	if _, _, ok := a.dockerRootFor(p); ok {
		return true
	}
	for _, r := range managedRoots {
		if within(p, r) {
			return true
		}
	}
	if _, rel, ok := splitHome(p); ok && within(rel, rootlessPodman) {
		return true
	}
	return false
}

func (a *Annotator) matchDevArtifact(q *query) (Annotation, bool) {
	if !q.isDir {
		return Annotation{}, false
	}
	for i := range devArtifacts {
		d := &devArtifacts[i]
		if d.name != q.name {
			continue
		}
		if a.inManagedStorage(q.path) {
			return Annotation{}, false
		}
		if d.marker != "" {
			base := q.dir
			if d.inside {
				base = q.path
			}
			if !a.isFile(base + "/" + d.marker) {
				return Annotation{}, false
			}
		}
		return d.ann, true
	}
	return Annotation{}, false
}
