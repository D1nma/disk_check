package annotate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// hexID returns a deterministic 64-hex-digit ID.
func hexID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

type fakeHost struct {
	t    *testing.T
	root string
}

func (h fakeHost) file(rel, content string) {
	h.t.Helper()
	p := filepath.Join(h.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h fakeHost) dir(rel string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Join(h.root, rel), 0o755); err != nil {
		h.t.Fatal(err)
	}
}

// Fixture IDs.
var (
	cidPG      = hexID("container-pg")
	cidEvil    = hexID("container-evil")
	cidNoCfg   = hexID("container-nocfg")
	mountPG    = hexID("mount-pg")
	mountEvil  = hexID("mount-evil")
	mountNoCfg = hexID("mount-nocfg")

	diff0       = "sha256:" + hexID("diff0")
	diff1       = "sha256:" + hexID("diff1")
	imgNginx    = hexID("image-nginx")  // layers diff0, diff1; tags nginx:1.25, nginx:latest
	imgDebian   = hexID("image-debian") // layer diff0; tag debian:12
	cacheBase   = hexID("cache-base")
	cacheTop    = hexID("cache-top")
	cacheOrphan = hexID("cache-orphan")
	unknownLyr  = hexID("unknown-layer")

	podCID     = hexID("podman-container")
	podLayer   = hexID("podman-layer-rw")
	podImgLyr  = hexID("podman-layer-ro")
	podUnknown = hexID("podman-layer-unknown")

	podUID     = "1b4e28ba-2fa1-11d2-883f-0016d3cca427"
	evilPodUID = "6fa459ea-ee8a-3ca4-894e-db77e160355e"
)

// chain1Hex is the chain ID of nginx's top layer, computed here
// independently of the package: sha256("<chain0> <diff1>").
func chain1Hex() string {
	sum := sha256.Sum256([]byte(diff0 + " " + diff1))
	return hex.EncodeToString(sum[:])
}

// newFakeHost builds a host tree with Docker, podman, Kubernetes and
// developer metadata and returns its root.
func newFakeHost(t *testing.T) string {
	t.Helper()
	h := fakeHost{t: t, root: t.TempDir()}
	const dr = "var/lib/docker"

	// Docker containers and their writable layers.
	h.file(dr+"/containers/"+cidPG+"/config.v2.json",
		`{"ID":"`+cidPG+`","Name":"/postgres-prod","Config":{"Env":["PGDATA=/var/lib/postgresql/data"]}}`)
	h.file(dr+"/containers/"+cidPG+"/"+cidPG+"-json.log", "{}\n")
	h.file(dr+"/containers/"+cidEvil+"/config.v2.json", `{"Name":"/\u001b[31mevil\u001b[0m"}`)
	h.dir(dr + "/containers/" + cidNoCfg)
	h.file(dr+"/image/overlay2/layerdb/mounts/"+cidPG+"/mount-id", mountPG)
	h.file(dr+"/image/overlay2/layerdb/mounts/"+cidPG+"/init-id", mountPG+"-init")
	h.file(dr+"/image/overlay2/layerdb/mounts/"+cidEvil+"/mount-id", mountEvil+"\n")
	h.file(dr+"/image/overlay2/layerdb/mounts/"+cidNoCfg+"/mount-id", mountNoCfg)
	h.file(dr+"/image/overlay2/layerdb/mounts/not-an-id/mount-id", unknownLyr)

	// Docker image layers and tags.
	h.file(dr+"/image/overlay2/layerdb/sha256/"+strings.TrimPrefix(diff0, "sha256:")+"/cache-id", cacheBase)
	h.file(dr+"/image/overlay2/layerdb/sha256/"+chain1Hex()+"/cache-id", cacheTop)
	h.file(dr+"/image/overlay2/layerdb/sha256/"+hexID("chain-orphan")+"/cache-id", cacheOrphan)
	h.file(dr+"/image/overlay2/repositories.json", `{"Repositories":{`+
		`"nginx":{"nginx:latest":"sha256:`+imgNginx+`","nginx:1.25":"sha256:`+imgNginx+`","nginx@sha256:`+hexID("digest")+`":"sha256:`+imgNginx+`"},`+
		`"debian":{"debian:12":"sha256:`+imgDebian+`"},`+
		`"evil":{"evil:../../etc":"sha256:../../../etc/passwd"}}}`)
	h.file(dr+"/image/overlay2/imagedb/content/sha256/"+imgNginx,
		`{"architecture":"amd64","rootfs":{"type":"layers","diff_ids":["`+diff0+`","`+diff1+`"]}}`)
	h.file(dr+"/image/overlay2/imagedb/content/sha256/"+imgDebian,
		`{"rootfs":{"type":"layers","diff_ids":["`+diff0+`"]}}`)

	// Podman (rootful).
	const ps = "var/lib/containers/storage"
	h.file(ps+"/overlay-containers/containers.json",
		`[{"id":"`+podCID+`","names":["web"],"image":"`+hexID("img")+`","layer":"`+podLayer+`"}]`)
	h.file(ps+"/overlay-layers/layers.json",
		`[{"id":"`+podLayer+`","parent":"`+podImgLyr+`"},{"id":"`+podImgLyr+`"}]`)

	// Kubernetes.
	h.dir("var/log/pods/default_api-7f9c_" + podUID + "/api")
	h.dir("var/log/pods/kube-system_evil\x1b]0;pwned\x07_" + evilPodUID)
	h.dir("var/log/pods/garbage")

	// Developer artifacts.
	h.file("home/bob/proj/Cargo.toml", "[package]\n")
	h.dir("home/bob/proj/target")
	h.dir("home/bob/other/target")
	h.file("srv/app/package.json", "{}")
	h.dir("srv/app/node_modules")
	h.file("srv/app/.venv/pyvenv.cfg", "home = /usr/bin\n")
	h.dir("srv/app/venv")
	h.file(dr+"/overlay2/"+mountPG+"/diff/app/package.json", "{}")
	h.file(dr+"/overlay2/"+mountPG+"/diff/app/Cargo.toml", "")

	return h.root
}

type lookupCase struct {
	path    string
	isDir   bool
	ok      bool
	label   string
	cleanup string
	regen   bool
}

func lookupCases() []lookupCase {
	const d = "/var/lib/docker"
	logPath := d + "/containers/" + cidPG + "/" + cidPG + "-json.log"
	pgWrite := Annotation{Label: "conteneur postgres-prod (couche d'écriture)", Cleanup: "inspecter : docker diff postgres-prod  (recréer le conteneur libère l'espace)"}
	podWrite := "inspecter : podman diff web  (recréer le conteneur libère l'espace)"
	snap := "/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots"
	k3sSnap := "/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots"
	rotated := "vérifier logrotate ; supprimable si plus utile"
	ctrdPrune := "crictl rmi --prune  (Docker : docker image prune -a)"
	return []lookupCase{
		// Docker: data root and its main directories.
		{path: d, isDir: true, ok: true, label: "données Docker", cleanup: "docker system df  puis  docker system prune"},
		{path: d + "/buildkit", isDir: true, ok: true, label: "cache de build Docker", cleanup: "docker builder prune", regen: true},
		{path: d + "/image", isDir: true, ok: true, label: "métadonnées des images Docker", cleanup: "docker image prune -a  (images inutilisées)"},
		{path: d + "/overlay2", isDir: true, ok: true, label: "couches overlay2 Docker", cleanup: "docker system df  puis  docker system prune"},
		{path: d + "/containers", isDir: true, ok: true, label: "conteneurs Docker (config et logs)", cleanup: "docker container prune  (conteneurs arrêtés)"},

		// Docker: container writable layers.
		{path: d + "/overlay2/" + mountPG, isDir: true, ok: true, label: pgWrite.Label, cleanup: pgWrite.Cleanup},
		{path: d + "/overlay2/" + mountPG + "/diff", isDir: true, ok: true, label: pgWrite.Label, cleanup: pgWrite.Cleanup},
		{path: d + "/overlay2/" + mountPG + "/work", isDir: true, ok: true, label: pgWrite.Label, cleanup: pgWrite.Cleanup},
		{path: d + "/overlay2/" + mountPG + "/merged", isDir: true, ok: true, label: pgWrite.Label + ", point de montage"},
		{path: d + "/overlay2/" + mountPG + "-init", isDir: true, ok: true, label: "conteneur postgres-prod (init)"},
		{path: d + "/overlay2/" + mountPG + "-init/diff", isDir: true, ok: true, label: "conteneur postgres-prod (init)"},
		{path: d + "/overlay2/" + mountNoCfg, isDir: true, ok: true,
			label:   "conteneur " + shortID(cidNoCfg) + " (couche d'écriture)",
			cleanup: "inspecter : docker diff " + shortID(cidNoCfg) + "  (recréer le conteneur libère l'espace)"},
		{path: d + "/overlay2/" + mountPG + "/diff/var", isDir: true, ok: false},
		{path: d + "/overlay2/" + mountPG + "/lower", isDir: false, ok: false},
		{path: d + "/overlay2/l", isDir: true, ok: false},

		// Docker: image layers, named after the images using them.
		{path: d + "/overlay2/" + cacheTop, isDir: true, ok: true, label: "couche d'image nginx:1.25", cleanup: "docker image prune -a  (images inutilisées)"},
		{path: d + "/overlay2/" + cacheBase, isDir: true, ok: true, label: "couche d'image debian:12 (+1 image)", cleanup: "docker image prune -a  (images inutilisées)"},
		{path: d + "/overlay2/" + cacheBase + "/diff", isDir: true, ok: true, label: "couche d'image debian:12 (+1 image)", cleanup: "docker image prune -a  (images inutilisées)"},
		{path: d + "/overlay2/" + cacheOrphan, isDir: true, ok: true, label: "couche d'image", cleanup: "docker image prune -a  (images inutilisées)"},
		{path: d + "/overlay2/" + unknownLyr, isDir: true, ok: true, label: "couche overlay2 docker", cleanup: "docker system df"},
		{path: d + "/overlay2/" + unknownLyr + "-init", isDir: true, ok: true, label: "couche overlay2 docker", cleanup: "docker system df"},

		// Docker: containers directory and logs.
		{path: d + "/containers/" + cidPG, isDir: true, ok: true, label: "conteneur postgres-prod", cleanup: "si arrêté : docker rm postgres-prod"},
		{path: logPath, isDir: false, ok: true, label: "log du conteneur postgres-prod",
			cleanup: "limiter les logs : --log-opt max-size=… (daemon.json) ; vider : truncate -s 0 " + logPath},
		{path: logPath + ".1", isDir: false, ok: true, label: "log archivé du conteneur postgres-prod",
			cleanup: "limiter les logs : --log-opt max-size=… max-file=… (daemon.json)"},
		{path: d + "/containers/" + cidPG + "/hostconfig.json", isDir: false, ok: false},
		{path: logPath, isDir: true, ok: false},
		{path: d + "/containers/not-a-container", isDir: true, ok: false},

		// Docker: volumes.
		{path: d + "/volumes", isDir: true, ok: true, label: "volumes Docker", cleanup: "docker volume ls -f dangling=true  (volumes orphelins)"},
		{path: d + "/volumes/pgdata", isDir: true, ok: true, label: "volume docker pgdata", cleanup: "utilisé ? docker ps -a --filter volume=pgdata"},
		{path: d + "/volumes/pgdata/_data", isDir: true, ok: true, label: "volume docker pgdata", cleanup: "utilisé ? docker ps -a --filter volume=pgdata"},
		{path: d + "/volumes/" + unknownLyr, isDir: true, ok: true, label: "volume docker anonyme " + shortID(unknownLyr), cleanup: "utilisé ? docker ps -a --filter volume=" + unknownLyr},
		{path: d + "/volumes/metadata.db", isDir: false, ok: false},

		// Rootless Docker.
		{path: "/home/alice/.local/share/docker", isDir: true, ok: true, label: "données Docker", cleanup: "docker system df  puis  docker system prune"},

		// Podman (rootful, metadata) and rootless (generic).
		{path: "/var/lib/containers/storage", isDir: true, ok: true, label: "stockage podman / CRI-O", cleanup: "podman system df  puis  podman system prune"},
		{path: "/var/lib/containers/storage/overlay/" + podLayer, isDir: true, ok: true, label: "conteneur podman web (couche d'écriture)", cleanup: podWrite},
		{path: "/var/lib/containers/storage/overlay/" + podLayer + "/merged", isDir: true, ok: true, label: "conteneur podman web (couche d'écriture), point de montage"},
		{path: "/var/lib/containers/storage/overlay/" + podImgLyr, isDir: true, ok: true, label: "couche d'image podman", cleanup: "podman system prune"},
		{path: "/var/lib/containers/storage/overlay/" + podUnknown, isDir: true, ok: true, label: "couche overlay podman", cleanup: "podman system df"},
		{path: "/var/lib/containers/storage/overlay/l", isDir: true, ok: false},
		{path: "/home/alice/.local/share/containers/storage", isDir: true, ok: true, label: "stockage podman rootless", cleanup: "podman system prune  (en tant que cet utilisateur)"},
		{path: "/home/alice/.local/share/containers/storage/overlay/" + podLayer, isDir: true, ok: true, label: "couche podman (rootless)", cleanup: "podman system prune"},

		// containerd.
		{path: snap + "/42", isDir: true, ok: true, label: "snapshot containerd", cleanup: ctrdPrune},
		{path: snap + "/42/fs", isDir: true, ok: true, label: "snapshot containerd", cleanup: ctrdPrune},
		{path: snap, isDir: true, ok: true, label: "snapshots containerd", cleanup: ctrdPrune},
		{path: k3sSnap + "/7", isDir: true, ok: true, label: "snapshot containerd", cleanup: "k3s crictl rmi --prune"},
		{path: snap + "/abc", isDir: true, ok: false},
		{path: snap + "/42/fs/usr", isDir: true, ok: false},

		// Kubernetes.
		{path: "/var/lib/kubelet/pods/" + podUID, isDir: true, ok: true, label: "pod default/api-7f9c"},
		{path: "/var/lib/kubelet/pods/" + podUID + "/volumes/kubernetes.io~empty-dir/cache", isDir: true, ok: true,
			label: "emptyDir cache du pod default/api-7f9c", cleanup: "libéré à la suppression du pod : kubectl -n default delete pod api-7f9c"},
		{path: "/var/lib/kubelet/pods/0a0a0a0a-0000-0000-0000-000000000000", isDir: true, ok: true, label: "pod kubernetes 0a0a0a0a"},
		{path: "/var/log/pods/default_api-7f9c_" + podUID, isDir: true, ok: true, label: "logs du pod default/api-7f9c",
			cleanup: "rotation par kubelet : containerLogMaxSize / containerLogMaxFiles"},
		{path: "/var/log/pods", isDir: true, ok: true, label: "logs des pods Kubernetes", cleanup: "rotation par kubelet : containerLogMaxSize / containerLogMaxFiles"},
		{path: "/var/log/pods/garbage", isDir: true, ok: false},

		// System.
		{path: "/var/log/journal", isDir: true, ok: true, label: "journal systemd", cleanup: "journalctl --vacuum-size=500M"},
		{path: "/var/log/journal/0123456789abcdef0123456789abcdef", isDir: true, ok: true, label: "journal systemd", cleanup: "journalctl --vacuum-size=500M"},
		{path: "/var/cache/apt/archives", isDir: true, ok: true, label: "cache de paquets apt", cleanup: "apt-get clean", regen: true},
		{path: "/var/cache/dnf", isDir: true, ok: true, label: "cache de paquets dnf", cleanup: "dnf clean all", regen: true},
		{path: "/var/cache/yum", isDir: true, ok: true, label: "cache de paquets yum", cleanup: "yum clean all", regen: true},
		{path: "/var/cache/zypp", isDir: true, ok: true, label: "cache de paquets zypper", cleanup: "zypper clean", regen: true},
		{path: "/var/cache/pacman/pkg", isDir: true, ok: true, label: "cache de paquets pacman", cleanup: "paccache -r", regen: true},
		{path: "/var/lib/systemd/coredump", isDir: true, ok: true, label: "core dumps", cleanup: "coredumpctl list ; supprimer les anciens dumps"},
		{path: "/var/lib/systemd/coredump/core.bash.1000.zst", isDir: false, ok: true, label: "core dump", cleanup: "coredumpctl list ; supprimer les anciens dumps"},
		{path: "/var/crash", isDir: true, ok: true, label: "core dumps", cleanup: "coredumpctl list ; supprimer les anciens dumps"},
		{path: "/var/lib/snapd/snaps", isDir: true, ok: true, label: "paquets snap (révisions)", cleanup: "snap set system refresh.retain=2"},
		{path: "/nix/store", isDir: true, ok: true, label: "store Nix", cleanup: "nix-collect-garbage -d"},
		{path: "/var/lib/flatpak", isDir: true, ok: true, label: "flatpak (applications et runtimes)", cleanup: "flatpak uninstall --unused"},
		{path: "/tmp", isDir: true, ok: true, label: "fichiers temporaires", cleanup: "systemd-tmpfiles --clean"},
		{path: "/var/tmp/", isDir: true, ok: true, label: "fichiers temporaires", cleanup: "systemd-tmpfiles --clean"},
		{path: "/var/cache/apt/archives", isDir: false, ok: false},

		// Rotated logs (files under /var/log only).
		{path: "/var/log/syslog.1", isDir: false, ok: true, label: "log archivé (rotation)", cleanup: rotated},
		{path: "/var/log/nginx/access.log.3.gz", isDir: false, ok: true, label: "log archivé (rotation)", cleanup: rotated},
		{path: "/var/log/messages-20240107", isDir: false, ok: true, label: "log archivé (rotation)", cleanup: rotated},
		{path: "/var/log/app/app.log.old", isDir: false, ok: true, label: "log archivé (rotation)", cleanup: rotated},
		{path: "/var/log/kern.log.2.zst", isDir: false, ok: true, label: "log archivé (rotation)", cleanup: rotated},
		{path: "/var/log/syslog", isDir: false, ok: false},
		{path: "/var/log/syslog.1", isDir: true, ok: false},
		{path: "/home/bob/backup.gz", isDir: false, ok: false},

		// Home caches.
		{path: "/home/bob/.cache", isDir: true, ok: true, label: "cache utilisateur", regen: true},
		{path: "/root/.cache", isDir: true, ok: true, label: "cache utilisateur", regen: true},
		{path: "/home/bob/.cache/go-build", isDir: true, ok: true, label: "cache de build Go", cleanup: "go clean -cache", regen: true},
		{path: "/home/bob/.cache/pip", isDir: true, ok: true, label: "cache pip", cleanup: "pip cache purge", regen: true},
		{path: "/root/.npm/_cacache", isDir: true, ok: true, label: "cache npm", cleanup: "npm cache clean --force", regen: true},
		{path: "/home/bob/.gradle/caches", isDir: true, ok: true, label: "cache Gradle", regen: true},
		{path: "/home/bob/.m2/repository", isDir: true, ok: true, label: "dépôt Maven local", regen: true},
		{path: "/home/bob/.local/share/Trash", isDir: true, ok: true, label: "corbeille", cleanup: "vider la corbeille"},
		{path: "/Users/bob/Library/Caches", isDir: true, ok: true, label: "cache utilisateur (macOS)", regen: true},
		{path: "/home/bob/proj/.cache", isDir: true, ok: false},
		{path: "/home/bob/.cache/thumbnails", isDir: true, ok: false},

		// Developer artifacts.
		{path: "/home/bob/proj/target", isDir: true, ok: true, label: "build Rust", cleanup: "cargo clean", regen: true},
		{path: "/home/bob/other/target", isDir: true, ok: false},
		{path: "/srv/app/node_modules", isDir: true, ok: true, label: "dépendances npm", cleanup: "npm ci pour reconstruire", regen: true},
		{path: "/usr/lib/node_modules", isDir: true, ok: false},
		{path: "/srv/app/__pycache__", isDir: true, ok: true, label: "cache bytecode Python", regen: true},
		{path: "/srv/app/.venv", isDir: true, ok: true, label: "virtualenv Python", regen: true},
		{path: "/srv/app/venv", isDir: true, ok: false},
		{path: d + "/overlay2/" + mountPG + "/diff/app/node_modules", isDir: true, ok: false},
		{path: d + "/overlay2/" + mountPG + "/diff/app/target", isDir: true, ok: false},
		{path: "/var/lib/containers/storage/overlay/" + podLayer + "/diff/__pycache__", isDir: true, ok: false},

		// Unrelated paths.
		{path: "/", isDir: true, ok: false},
		{path: "/etc", isDir: true, ok: false},
		{path: "/usr/bin/ls", isDir: false, ok: false},
		{path: "/home/bob", isDir: true, ok: false},
		{path: "/srv/data/x9f3", isDir: true, ok: false},
		{path: "var/log/journal", isDir: true, ok: false},
		{path: "", isDir: true, ok: false},
	}
}

func checkCase(t *testing.T, a *Annotator, c lookupCase) {
	t.Helper()
	got, ok := a.Lookup(c.path, c.isDir)
	if ok != c.ok {
		t.Errorf("Lookup(%q, %v): ok = %v (%+v), want %v", c.path, c.isDir, ok, got, c.ok)
		return
	}
	if !ok {
		if got != (Annotation{}) {
			t.Errorf("Lookup(%q, %v): not ok but returned %+v", c.path, c.isDir, got)
		}
		return
	}
	want := Annotation{Label: c.label, Cleanup: c.cleanup, Regen: c.regen}
	if got != want {
		t.Errorf("Lookup(%q, %v)\n got  %+v\n want %+v", c.path, c.isDir, got, want)
	}
}

func TestLookup(t *testing.T) {
	a := New(Options{Root: newFakeHost(t)})
	for _, c := range lookupCases() {
		checkCase(t, a, c)
	}
	// Cached results are identical.
	for _, c := range lookupCases() {
		checkCase(t, a, c)
	}
}

func TestLookupWithoutMetadata(t *testing.T) {
	// Empty host: container paths still get generic labels, nothing fails.
	a := New(Options{Root: t.TempDir()})
	cases := []lookupCase{
		{path: "/var/lib/docker/overlay2/" + mountPG, isDir: true, ok: true, label: "couche overlay2 docker", cleanup: "docker system df"},
		{path: "/var/lib/docker/containers/" + cidPG, isDir: true, ok: true,
			label: "conteneur " + shortID(cidPG), cleanup: "si arrêté : docker rm " + shortID(cidPG)},
		{path: "/var/lib/containers/storage/overlay/" + podLayer, isDir: true, ok: true, label: "couche overlay podman", cleanup: "podman system df"},
		{path: "/var/lib/kubelet/pods/" + podUID, isDir: true, ok: true, label: "pod kubernetes 1b4e28ba"},
		{path: "/srv/app/node_modules", isDir: true, ok: false},
	}
	for _, c := range cases {
		checkCase(t, a, c)
	}
}

func TestDockerDataRootFromDaemonJSON(t *testing.T) {
	h := fakeHost{t: t, root: t.TempDir()}
	const dr = "/srv/docker data"
	h.file("etc/docker/daemon.json", `{"data-root": "`+dr+`/", "log-driver": "json-file"}`)
	h.file(dr+"/image/overlay2/layerdb/mounts/"+cidPG+"/mount-id", mountPG)
	h.file(dr+"/containers/"+cidPG+"/config.v2.json", `{"Name":"/postgres-prod"}`)
	a := New(Options{Root: h.root})

	checkCase(t, a, lookupCase{path: dr, isDir: true, ok: true, label: "données Docker", cleanup: "docker system df  puis  docker system prune"})
	checkCase(t, a, lookupCase{path: dr + "/overlay2/" + mountPG, isDir: true, ok: true,
		label:   "conteneur postgres-prod (couche d'écriture)",
		cleanup: "inspecter : docker diff postgres-prod  (recréer le conteneur libère l'espace)"})
	// The path holds a space: the suggested command quotes it.
	logPath := dr + "/containers/" + cidPG + "/" + cidPG + "-json.log"
	checkCase(t, a, lookupCase{path: logPath, isDir: false, ok: true, label: "log du conteneur postgres-prod",
		cleanup: "limiter les logs : --log-opt max-size=… (daemon.json) ; vider : truncate -s 0 '" + logPath + "'"})
	// The default root is still recognised (data left behind by a move).
	checkCase(t, a, lookupCase{path: "/var/lib/docker", isDir: true, ok: true, label: "données Docker", cleanup: "docker system df  puis  docker system prune"})
}

func TestUntrustedNamesAreSanitised(t *testing.T) {
	root := newFakeHost(t)
	long := strings.Repeat("é", 100)
	h := fakeHost{t: t, root: root}
	cidLong := hexID("container-long")
	h.file("var/lib/docker/containers/"+cidLong+"/config.v2.json", `{"Name":"/`+long+`"}`)
	a := New(Options{Root: root})

	evil := []struct {
		path  string
		isDir bool
	}{
		{"/var/lib/docker/overlay2/" + mountEvil, true},
		{"/var/lib/docker/containers/" + cidEvil, true},
		{"/var/lib/docker/containers/" + cidEvil + "/" + cidEvil + "-json.log", false},
		{"/var/lib/kubelet/pods/" + evilPodUID, true},
		{"/var/lib/kubelet/pods/" + evilPodUID + "/volumes/kubernetes.io~empty-dir/x\x1b[2Jy", true},
		{"/var/log/pods/kube-system_evil\x1b]0;pwned\x07_" + evilPodUID, true},
		{"/var/lib/docker/volumes/vol\x1b[31m\nname", true},
	}
	for _, e := range evil {
		got, ok := a.Lookup(e.path, e.isDir)
		if !ok {
			t.Errorf("Lookup(%q): no annotation", e.path)
			continue
		}
		for _, s := range []string{got.Label, got.Cleanup} {
			if strings.ContainsAny(s, "\x1b\x07\n\r") {
				t.Errorf("Lookup(%q): control character in %q", e.path, s)
			}
		}
	}

	// The container name keeps its printable part; commands fall back to the
	// container ID since the name is not shell-safe.
	got, _ := a.Lookup("/var/lib/docker/overlay2/"+mountEvil, true)
	if !strings.Contains(got.Label, "evil") {
		t.Errorf("label %q lost the printable part of the name", got.Label)
	}
	if want := "docker diff " + shortID(cidEvil); !strings.Contains(got.Cleanup, want) {
		t.Errorf("cleanup %q, want it to contain %q", got.Cleanup, want)
	}
	// A pod with an unsafe name gets no kubectl command.
	got, _ = a.Lookup("/var/lib/kubelet/pods/"+evilPodUID+"/volumes/kubernetes.io~empty-dir/data", true)
	if strings.Contains(got.Cleanup, "kubectl") {
		t.Errorf("cleanup %q suggests kubectl for an unsafe pod name", got.Cleanup)
	}
	// An overlong name is truncated on a rune boundary.
	got, _ = a.Lookup("/var/lib/docker/containers/"+cidLong, true)
	if len(got.Label) > len("conteneur ")+maxNameLen+len("…") || !strings.HasSuffix(got.Label, "…") {
		t.Errorf("long name not truncated: %q (%d bytes)", got.Label, len(got.Label))
	}
	if !strings.HasPrefix(got.Label, "conteneur éé") || strings.ContainsRune(got.Label, '�') {
		t.Errorf("long name truncated badly: %q", got.Label)
	}
}

func TestMetadataLoadedOnce(t *testing.T) {
	a := New(Options{Root: newFakeHost(t)})
	const d = "/var/lib/docker"
	lookup := func(p string, isDir bool) int64 {
		t.Helper()
		before := a.reads.Load()
		if _, ok := a.Lookup(p, isDir); !ok {
			t.Fatalf("Lookup(%q): no annotation", p)
		}
		return a.reads.Load() - before
	}

	if n := lookup(d+"/overlay2/"+mountPG, true); n == 0 {
		t.Fatal("first lookup read no metadata")
	}
	if n := lookup(d+"/overlay2/"+mountPG, true); n != 0 {
		t.Errorf("repeated lookup: %d metadata reads, want 0 (cached)", n)
	}
	// New paths needing already-loaded metadata: no I/O.
	if n := lookup(d+"/overlay2/"+mountPG+"/diff", true); n != 0 {
		t.Errorf("child of a known layer: %d reads, want 0", n)
	}
	if n := lookup(d+"/containers/"+cidPG, true); n != 0 {
		t.Errorf("known container: %d reads, want 0", n)
	}
	// Another container: only its config.v2.json is read.
	if n := lookup(d+"/overlay2/"+mountEvil, true); n != 1 {
		t.Errorf("second container: %d reads, want 1", n)
	}
	// Image layers: the layer database and tags are loaded on the first one only.
	if n := lookup(d+"/overlay2/"+cacheTop, true); n == 0 {
		t.Error("first image layer read no metadata")
	}
	if n := lookup(d+"/overlay2/"+cacheBase, true); n != 0 {
		t.Errorf("second image layer: %d reads, want 0", n)
	}
	// Podman: containers.json once, layers.json once.
	ps := "/var/lib/containers/storage/overlay/"
	if n := lookup(ps+podLayer, true); n != 1 {
		t.Errorf("podman container layer: %d reads, want 1 (containers.json)", n)
	}
	if n := lookup(ps+podImgLyr, true); n != 1 {
		t.Errorf("podman image layer: %d reads, want 1 (layers.json)", n)
	}
	if n := lookup(ps+podUnknown, true); n != 0 {
		t.Errorf("podman unknown layer: %d reads, want 0", n)
	}
	// Kubernetes: /var/log/pods listed once.
	if n := lookup("/var/lib/kubelet/pods/"+podUID, true); n != 1 {
		t.Errorf("first pod: %d reads, want 1", n)
	}
	if n := lookup("/var/lib/kubelet/pods/"+evilPodUID, true); n != 0 {
		t.Errorf("second pod: %d reads, want 0", n)
	}
	// Rules needing no metadata do no I/O at all.
	if n := lookup("/var/log/journal", true); n != 0 {
		t.Errorf("static rule: %d reads, want 0", n)
	}
	// A marker check (Cargo.toml) runs once; the per-path cache answers after.
	if n := lookup("/home/bob/proj/target", true); n != 1 {
		t.Errorf("Cargo target: %d reads, want 1 (Cargo.toml stat)", n)
	}
	if n := lookup("/home/bob/proj/target", true); n != 0 {
		t.Errorf("repeated Cargo target: %d reads, want 0 (per-path cache)", n)
	}
}

func TestConcurrentLookup(t *testing.T) {
	a := New(Options{Root: newFakeHost(t)})
	cases := lookupCases()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range cases {
				// Each goroutine walks the cases from a different offset so
				// that first loads race with each other.
				checkCase(t, a, cases[(i+g*7)%len(cases)])
			}
		}(g)
	}
	wg.Wait()
}

func TestCacheIsBounded(t *testing.T) {
	a := New(Options{Root: t.TempDir()})
	for i := 0; i < maxCacheEntries+100; i++ {
		a.Lookup(fmt.Sprintf("/data/dir%d", i), true)
	}
	a.mu.RLock()
	n := len(a.cache)
	a.mu.RUnlock()
	if n > maxCacheEntries {
		t.Errorf("cache holds %d entries, want <= %d", n, maxCacheEntries)
	}
	if _, ok := a.Lookup("/var/log/journal", true); !ok {
		t.Error("lookup after cache reset failed")
	}
}

func TestNilAnnotator(t *testing.T) {
	var a *Annotator
	if got, ok := a.Lookup("/var/log/journal", true); ok || got != (Annotation{}) {
		t.Errorf("nil Annotator: got %+v, %v", got, ok)
	}
}

func TestDarwinPrivatePrefix(t *testing.T) {
	a := New(Options{Root: t.TempDir()})
	a.darwin = true
	checkCase(t, a, lookupCase{path: "/private/var/log/system.log.0.gz", isDir: false, ok: true,
		label: "log archivé (rotation)", cleanup: "vérifier logrotate ; supprimable si plus utile"})
	checkCase(t, a, lookupCase{path: "/private/tmp", isDir: true, ok: true, label: "fichiers temporaires", cleanup: "systemd-tmpfiles --clean"})
	checkCase(t, a, lookupCase{path: "/private/variable", isDir: true, ok: false})
}

func TestIsRotatedLogName(t *testing.T) {
	cases := map[string]bool{
		"syslog.1":           true,
		"app.log.3.gz":       true,
		"auth.log.2.xz":      true,
		"kern.log.1.zst":     true,
		"dmesg.0":            true,
		"wtmp.1":             true,
		"debug.bz2":          true,
		"x.old":              true,
		"messages-20240107":  true,
		"secure-2024010712":  true,
		"syslog":             false,
		"app.log":            false,
		".gz":                false,
		"1":                  false,
		"README":             false,
		"backup-12345678":    false,
		"Xorg.0.log":         false,
		"cloud-init.log":     false,
		"lastlog":            false,
		"php8.2-fpm.log":     false,
		"vmware-network.log": false,
	}
	for name, want := range cases {
		if got := isRotatedLogName(name); got != want {
			t.Errorf("isRotatedLogName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestShellPath(t *testing.T) {
	cases := map[string]string{
		"/var/lib/docker/containers/abc/abc-json.log": "/var/lib/docker/containers/abc/abc-json.log",
		"/srv/my docker/x.log":                        "'/srv/my docker/x.log'",
		"/srv/it's/x.log":                             `'/srv/it'\''s/x.log'`,
		"/srv/\x1b[31m/x.log":                         "",
	}
	for in, want := range cases {
		if got := shellPath(in); got != want {
			t.Errorf("shellPath(%q) = %q, want %q", in, got, want)
		}
	}
}
