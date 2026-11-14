package annotate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Cleanup advice shared by container-storage rules.
const (
	cleanDockerSystem  = "docker system df  puis  docker system prune"
	cleanDockerDF      = "docker system df"
	cleanDockerImages  = "docker image prune -a  (images inutilisées)"
	cleanDockerBuilder = "docker builder prune"
	cleanPodmanPrune   = "podman system prune"
	cleanPodLogs       = "rotation par kubelet : containerLogMaxSize / containerLogMaxFiles"
)

// layerChild splits the components below a layer store directory
// (overlay2/<id>[/<child>]) and reports whether they designate the layer
// itself or one of its well-known sub-directories.
func layerChild(rest []string) (id, child string, ok bool) {
	switch len(rest) {
	case 1:
		return rest[0], "", true
	case 2:
		switch rest[1] {
		case "diff", "merged", "work":
			return rest[0], rest[1], true
		}
	}
	return "", "", false
}

// withChild adapts a layer annotation to one of its sub-directories. "merged"
// is the union mount point of a running container: it shows the whole
// container filesystem and must never be deleted by hand.
func withChild(ann Annotation, child string) Annotation {
	if child == "merged" {
		ann.Label += ", point de montage"
		ann.Cleanup = ""
		ann.Regen = false
	}
	return ann
}

// containerRef is a container name ready for display (label) and for a
// suggested command (arg: the name if shell-safe, else the short ID).
type containerRef struct {
	label string
	arg   string
}

func newContainerRef(name, id string) containerRef {
	short := shortID(id)
	if name == "" {
		return containerRef{label: short, arg: short}
	}
	ref := containerRef{label: displayName(name), arg: short}
	if isSafeArg(name) {
		ref.arg = name
	}
	return ref
}

// ===========================================================================
// Docker
// ===========================================================================

const dockerDefaultRoot = "/var/lib/docker"

// dockerExtraRoots are other well-known data roots checked besides the
// configured one: the default (still holding data after a data-root move)
// and the snap package's.
var dockerExtraRoots = [...]string{dockerDefaultRoot, "/var/snap/docker/common/var-lib-docker"}

// rootlessDocker is the data root of rootless Docker, relative to a home.
const rootlessDocker = ".local/share/docker"

// dockerRootList returns the system Docker data roots: "data-root" from
// /etc/docker/daemon.json (read once) first, then the well-known defaults.
func (a *Annotator) dockerRootList() []string {
	a.dockerRootsOnce.Do(func() {
		var roots []string
		if r := a.readDockerDataRoot(); r != "" {
			roots = append(roots, r)
		}
		for _, r := range dockerExtraRoots {
			if len(roots) == 0 || roots[0] != r {
				roots = append(roots, r)
			}
		}
		a.dockerRoots = roots
	})
	return a.dockerRoots
}

func (a *Annotator) readDockerDataRoot() string {
	b, err := a.readSmall("/etc/docker/daemon.json", maxConfigFile)
	if err != nil {
		return ""
	}
	var cfg struct {
		DataRoot string `json:"data-root"`
		Graph    string `json:"graph"` // pre-17.05 name of data-root
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	r := cfg.DataRoot
	if r == "" {
		r = cfg.Graph
	}
	if r == "" || r[0] != '/' {
		return ""
	}
	if r = filepath.Clean(r); r == "/" {
		return ""
	}
	return r
}

// dockerRootFor returns the Docker data root containing p, if any.
func (a *Annotator) dockerRootFor(p string) (root string, rest []string, ok bool) {
	for _, r := range a.dockerRootList() {
		if rest, ok := under(p, r); ok {
			return r, rest, true
		}
	}
	if home, rel, ok := splitHome(p); ok && within(rel, rootlessDocker) {
		r := home + "/" + rootlessDocker
		rest, _ := under(p, r)
		return r, rest, true
	}
	return "", nil, false
}

func (a *Annotator) matchDocker(q *query) (Annotation, bool) {
	root, rest, ok := a.dockerRootFor(q.path)
	if !ok {
		return Annotation{}, false
	}
	return a.dockerStore(root).annotate(q, rest)
}

func (a *Annotator) dockerStore(root string) *dockerStore {
	a.storesMu.Lock()
	defer a.storesMu.Unlock()
	s := a.docker[root]
	if s == nil {
		s = &dockerStore{a: a, root: root, names: make(map[string]containerRef)}
		a.docker[root] = s
	}
	return s
}

// dockerStore holds the lazily loaded metadata of one Docker data root.
type dockerStore struct {
	a    *Annotator
	root string

	mountsOnce sync.Once
	mounts     map[string]string // overlay2 directory of a container layer -> container ID

	layersOnce sync.Once
	layers     map[string]string // overlay2 directory of an image layer -> chain ID (hex)

	tagsOnce sync.Once
	tags     map[string]*layerImages // chain ID (hex) -> images using the layer

	namesMu sync.Mutex
	names   map[string]containerRef // container ID -> name, read on demand
}

func (s *dockerStore) layerDB() string { return s.root + "/image/overlay2/layerdb" }

func (s *dockerStore) annotate(q *query, rest []string) (Annotation, bool) {
	if len(rest) == 0 {
		if !q.isDir {
			return Annotation{}, false
		}
		return Annotation{Label: "données Docker", Cleanup: cleanDockerSystem}, true
	}
	switch rest[0] {
	case "containers":
		return s.containerPath(q, rest[1:])
	case "overlay2":
		return s.overlayPath(q, rest[1:])
	case "volumes":
		return s.volumePath(q, rest[1:])
	case "image":
		if len(rest) == 1 && q.isDir {
			return Annotation{Label: "métadonnées des images Docker", Cleanup: cleanDockerImages}, true
		}
	case "buildkit":
		if len(rest) == 1 && q.isDir {
			return Annotation{Label: "cache de build Docker", Cleanup: cleanDockerBuilder, Regen: true}, true
		}
	}
	return Annotation{}, false
}

// containerPath handles <root>/containers[/<cid>[/<cid>-json.log*]].
func (s *dockerStore) containerPath(q *query, rest []string) (Annotation, bool) {
	switch len(rest) {
	case 0:
		if q.isDir {
			return Annotation{Label: "conteneurs Docker (config et logs)", Cleanup: "docker container prune  (conteneurs arrêtés)"}, true
		}
	case 1:
		if q.isDir && isHexID(rest[0]) {
			c := s.containerName(rest[0])
			return Annotation{Label: "conteneur " + c.label, Cleanup: "si arrêté : docker rm " + c.arg}, true
		}
	case 2:
		cid, file := rest[0], rest[1]
		if q.isDir || !isHexID(cid) {
			break
		}
		logName := cid + "-json.log"
		switch {
		case file == logName:
			c := s.containerName(cid)
			cleanup := "limiter les logs : --log-opt max-size=… (daemon.json)"
			if sp := shellPath(q.path); sp != "" {
				cleanup += " ; vider : truncate -s 0 " + sp
			}
			return Annotation{Label: "log du conteneur " + c.label, Cleanup: cleanup}, true
		case strings.HasPrefix(file, logName+"."):
			c := s.containerName(cid)
			return Annotation{
				Label:   "log archivé du conteneur " + c.label,
				Cleanup: "limiter les logs : --log-opt max-size=… max-file=… (daemon.json)",
			}, true
		}
	}
	return Annotation{}, false
}

// overlayPath handles <root>/overlay2[/<id>[/diff|merged|work]].
func (s *dockerStore) overlayPath(q *query, rest []string) (Annotation, bool) {
	if !q.isDir {
		return Annotation{}, false
	}
	if len(rest) == 0 {
		return Annotation{Label: "couches overlay2 Docker", Cleanup: cleanDockerSystem}, true
	}
	id, child, ok := layerChild(rest)
	if !ok {
		return Annotation{}, false
	}
	base, isInit := strings.CutSuffix(id, "-init")
	if !isHexID(base) { // also skips overlay2/l, the short-name symlinks
		return Annotation{}, false
	}
	if cid, ok := s.mountOwner(base); ok {
		c := s.containerName(cid)
		if isInit {
			return withChild(Annotation{Label: "conteneur " + c.label + " (init)"}, child), true
		}
		return withChild(Annotation{
			Label:   "conteneur " + c.label + " (couche d'écriture)",
			Cleanup: "inspecter : docker diff " + c.arg + "  (recréer le conteneur libère l'espace)",
		}, child), true
	}
	if !isInit {
		if chain, ok := s.layerChain(base); ok {
			return withChild(Annotation{Label: s.imageLayerLabel(chain), Cleanup: cleanDockerImages}, child), true
		}
	}
	return withChild(Annotation{Label: "couche overlay2 docker", Cleanup: cleanDockerDF}, child), true
}

// volumePath handles <root>/volumes[/<name>[/_data]].
func (s *dockerStore) volumePath(q *query, rest []string) (Annotation, bool) {
	if !q.isDir {
		return Annotation{}, false
	}
	switch {
	case len(rest) == 0:
		return Annotation{Label: "volumes Docker", Cleanup: "docker volume ls -f dangling=true  (volumes orphelins)"}, true
	case len(rest) == 1, len(rest) == 2 && rest[1] == "_data":
		name := rest[0]
		if len(name) == 64 && isHexID(name) {
			return Annotation{Label: "volume docker anonyme " + shortID(name), Cleanup: "utilisé ? docker ps -a --filter volume=" + name}, true
		}
		ann := Annotation{Label: "volume docker " + displayName(name)}
		if isSafeArg(name) {
			ann.Cleanup = "utilisé ? docker ps -a --filter volume=" + name
		}
		return ann, true
	}
	return Annotation{}, false
}

// mountOwner returns the container whose writable layer is the overlay2
// directory id. The mapping comes from
// <root>/image/overlay2/layerdb/mounts/<containerID>/mount-id, loaded once.
func (s *dockerStore) mountOwner(id string) (string, bool) {
	s.mountsOnce.Do(func() {
		dir := s.layerDB() + "/mounts"
		names := s.a.listNames(dir)
		m := make(map[string]string, len(names))
		for _, cid := range names {
			if !isHexID(cid) {
				continue
			}
			b, err := s.a.readSmall(dir+"/"+cid+"/mount-id", maxIDFile)
			if err != nil {
				continue
			}
			if mid := strings.TrimSpace(string(b)); isHexID(mid) {
				m[mid] = cid
			}
		}
		s.mounts = m
	})
	cid, ok := s.mounts[id]
	return cid, ok
}

// layerChain returns the chain ID of the image layer stored in the overlay2
// directory id, from <root>/image/overlay2/layerdb/sha256/<chain>/cache-id,
// loaded once.
func (s *dockerStore) layerChain(id string) (string, bool) {
	s.layersOnce.Do(func() {
		dir := s.layerDB() + "/sha256"
		names := s.a.listNames(dir)
		m := make(map[string]string, len(names))
		for _, chain := range names {
			if !isHexID(chain) {
				continue
			}
			b, err := s.a.readSmall(dir+"/"+chain+"/cache-id", maxIDFile)
			if err != nil {
				continue
			}
			if cid := strings.TrimSpace(string(b)); isHexID(cid) {
				m[cid] = chain
			}
		}
		s.layers = m
	})
	chain, ok := s.layers[id]
	return chain, ok
}

// containerName returns the name of a container from
// <root>/containers/<cid>/config.v2.json, read at most once per container.
func (s *dockerStore) containerName(cid string) containerRef {
	s.namesMu.Lock()
	defer s.namesMu.Unlock()
	if c, ok := s.names[cid]; ok {
		return c
	}
	var name string
	if b, err := s.a.readSmall(s.root+"/containers/"+cid+"/config.v2.json", maxConfigFile); err == nil {
		var cfg struct {
			Name string `json:"Name"`
		}
		if json.Unmarshal(b, &cfg) == nil {
			name = strings.TrimPrefix(cfg.Name, "/")
		}
	}
	c := newContainerRef(name, cid)
	s.names[cid] = c
	return c
}

// layerImages records which tagged images use an image layer.
type layerImages struct {
	top    string // smallest tag of an image whose top layer this is
	first  string // smallest tag of any image using the layer
	images int    // number of tagged images using the layer
}

// imageLayerLabel names an image layer after the tagged image(s) using it.
func (s *dockerStore) imageLayerLabel(chain string) string {
	s.tagsOnce.Do(s.loadTags)
	li := s.tags[chain]
	if li == nil {
		return "couche d'image"
	}
	name := li.top
	if name == "" {
		name = li.first
	}
	switch others := li.images - 1; others {
	case 0:
		return "couche d'image " + name
	case 1:
		return "couche d'image " + name + " (+1 image)"
	default:
		return "couche d'image " + name + " (+" + strconv.Itoa(others) + " images)"
	}
}

// loadTags maps layer chain IDs to image tags: repositories.json gives
// tag -> image ID, each image config lists the layer diff IDs, from which
// the chain IDs (the layerdb directory names) are derived.
func (s *dockerStore) loadTags() {
	s.tags = make(map[string]*layerImages)
	b, err := s.a.readSmall(s.root+"/image/overlay2/repositories.json", maxIndexFile)
	if err != nil {
		return
	}
	var repos struct {
		Repositories map[string]map[string]string `json:"Repositories"`
	}
	if json.Unmarshal(b, &repos) != nil {
		return
	}
	imageTags := make(map[string][]string)
	for _, refs := range repos.Repositories {
		for ref, id := range refs {
			if strings.Contains(ref, "@") { // digest reference, not a tag
				continue
			}
			h, ok := strings.CutPrefix(id, "sha256:")
			if !ok || len(h) != 64 || !isHexID(h) {
				continue
			}
			imageTags[h] = append(imageTags[h], ref)
		}
	}
	ids := make([]string, 0, len(imageTags))
	for id := range imageTags {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > maxImages {
		ids = ids[:maxImages]
	}
	for _, id := range ids {
		tags := imageTags[id]
		sort.Strings(tags)
		tag := displayName(tags[0])
		diffIDs := s.readDiffIDs(id)
		var chain string
		for i, d := range diffIDs {
			if i == 0 {
				chain = d
			} else {
				chain = chainID(chain, d)
			}
			h := strings.TrimPrefix(chain, "sha256:")
			li := s.tags[h]
			if li == nil {
				li = &layerImages{}
				s.tags[h] = li
			}
			li.images++
			if li.first == "" || tag < li.first {
				li.first = tag
			}
			if i == len(diffIDs)-1 && (li.top == "" || tag < li.top) {
				li.top = tag
			}
		}
	}
}

// readDiffIDs returns the layer diff IDs of an image, from its config in
// <root>/image/overlay2/imagedb/content/sha256/<id>.
func (s *dockerStore) readDiffIDs(id string) []string {
	b, err := s.a.readSmall(s.root+"/image/overlay2/imagedb/content/sha256/"+id, maxConfigFile)
	if err != nil {
		return nil
	}
	var cfg struct {
		RootFS struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	for _, d := range cfg.RootFS.DiffIDs {
		if h, ok := strings.CutPrefix(d, "sha256:"); !ok || len(h) != 64 || !isHexID(h) {
			return nil
		}
	}
	return cfg.RootFS.DiffIDs
}

// chainID derives the chain ID of a layer from its parent's chain ID and its
// own diff ID, as Docker does (layer.CreateChainID).
func chainID(parent, diffID string) string {
	sum := sha256.Sum256([]byte(parent + " " + diffID))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ===========================================================================
// Podman / containers-storage (also used by CRI-O and Buildah)
// ===========================================================================

const (
	podmanRoot     = "/var/lib/containers/storage"
	rootlessPodman = ".local/share/containers/storage" // relative to a home
)

func (a *Annotator) matchPodman(q *query) (Annotation, bool) {
	if rest, ok := under(q.path, podmanRoot); ok {
		return a.podmanStore(podmanRoot).annotate(q, rest)
	}
	if _, rel, ok := splitHome(q.path); ok {
		if rest, ok := under(rel, rootlessPodman); ok {
			return annotateRootlessPodman(q, rest)
		}
	}
	return Annotation{}, false
}

func (a *Annotator) podmanStore(root string) *podmanStore {
	a.storesMu.Lock()
	defer a.storesMu.Unlock()
	s := a.podman[root]
	if s == nil {
		s = &podmanStore{a: a, root: root}
		a.podman[root] = s
	}
	return s
}

// podmanStore holds the lazily loaded metadata of a rootful storage root.
type podmanStore struct {
	a    *Annotator
	root string

	containersOnce sync.Once
	containers     map[string]containerRef // writable layer ID -> container

	layersOnce sync.Once
	layers     map[string]bool // known layer IDs
}

func (s *podmanStore) annotate(q *query, rest []string) (Annotation, bool) {
	if !q.isDir {
		return Annotation{}, false
	}
	if len(rest) == 0 {
		return Annotation{Label: "stockage podman / CRI-O", Cleanup: "podman system df  puis  podman system prune"}, true
	}
	if rest[0] != "overlay" {
		return Annotation{}, false
	}
	if len(rest) == 1 {
		return Annotation{Label: "couches overlay podman", Cleanup: cleanPodmanPrune}, true
	}
	id, child, ok := layerChild(rest[1:])
	if !ok || !isHexID(id) { // also skips overlay/l
		return Annotation{}, false
	}
	if c, ok := s.containerOf(id); ok {
		return withChild(Annotation{
			Label:   "conteneur podman " + c.label + " (couche d'écriture)",
			Cleanup: "inspecter : podman diff " + c.arg + "  (recréer le conteneur libère l'espace)",
		}, child), true
	}
	if s.isLayer(id) {
		return withChild(Annotation{Label: "couche d'image podman", Cleanup: cleanPodmanPrune}, child), true
	}
	return withChild(Annotation{Label: "couche overlay podman", Cleanup: "podman system df"}, child), true
}

// containerOf maps a writable layer to its container, from
// overlay-containers/containers.json (loaded once).
func (s *podmanStore) containerOf(layer string) (containerRef, bool) {
	s.containersOnce.Do(func() {
		s.containers = make(map[string]containerRef)
		b, err := s.a.readSmall(s.root+"/overlay-containers/containers.json", maxIndexFile)
		if err != nil {
			return
		}
		var list []struct {
			ID    string   `json:"id"`
			Names []string `json:"names"`
			Layer string   `json:"layer"`
		}
		if json.Unmarshal(b, &list) != nil {
			return
		}
		for _, c := range list {
			if c.Layer == "" {
				continue
			}
			var name string
			if len(c.Names) > 0 {
				name = c.Names[0]
			}
			s.containers[c.Layer] = newContainerRef(name, c.ID)
		}
	})
	c, ok := s.containers[layer]
	return c, ok
}

// isLayer reports whether id is listed in overlay-layers/layers.json
// (loaded once).
func (s *podmanStore) isLayer(id string) bool {
	s.layersOnce.Do(func() {
		s.layers = make(map[string]bool)
		b, err := s.a.readSmall(s.root+"/overlay-layers/layers.json", maxIndexFile)
		if err != nil {
			return
		}
		var list []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(b, &list) != nil {
			return
		}
		for _, l := range list {
			s.layers[l.ID] = true
		}
	})
	return s.layers[id]
}

// annotateRootlessPodman labels a rootless storage root without reading its
// metadata.
func annotateRootlessPodman(q *query, rest []string) (Annotation, bool) {
	if !q.isDir {
		return Annotation{}, false
	}
	switch {
	case len(rest) == 0:
		return Annotation{Label: "stockage podman rootless", Cleanup: "podman system prune  (en tant que cet utilisateur)"}, true
	case rest[0] != "overlay":
		return Annotation{}, false
	case len(rest) == 1:
		return Annotation{Label: "couches overlay podman (rootless)", Cleanup: cleanPodmanPrune}, true
	}
	id, child, ok := layerChild(rest[1:])
	if !ok || !isHexID(id) {
		return Annotation{}, false
	}
	return withChild(Annotation{Label: "couche podman (rootless)", Cleanup: cleanPodmanPrune}, child), true
}

// ===========================================================================
// containerd (plain, k3s, RKE2)
// ===========================================================================

// containerdRoots lists containerd data roots with their prune command. The
// plain root may also hold Docker's images (containerd image store, "moby"
// namespace), which crictl does not see.
var containerdRoots = [...]struct{ root, cleanup string }{
	{"/var/lib/containerd", "crictl rmi --prune  (Docker : docker image prune -a)"},
	{"/var/lib/rancher/k3s/agent/containerd", "k3s crictl rmi --prune"},
	{"/var/lib/rancher/rke2/agent/containerd", "crictl rmi --prune"},
}

const (
	containerdSnapshotter = "io.containerd.snapshotter.v1.overlayfs"
	containerdContent     = "io.containerd.content.v1.content"
)

// matchContainerd labels containerd snapshots. Snapshot numbers map to
// images and containers only through containerd's bolt database, which is
// not read: the label stays generic.
func (a *Annotator) matchContainerd(q *query) (Annotation, bool) {
	if !q.isDir {
		return Annotation{}, false
	}
	for _, cr := range containerdRoots {
		rest, ok := under(q.path, cr.root)
		if !ok {
			continue
		}
		switch {
		case len(rest) == 0:
			return Annotation{Label: "données containerd", Cleanup: cr.cleanup}, true
		case rest[0] == containerdContent && len(rest) == 1:
			return Annotation{Label: "blobs d'images containerd", Cleanup: cr.cleanup}, true
		case rest[0] != containerdSnapshotter:
			return Annotation{}, false
		case len(rest) == 1, len(rest) == 2 && rest[1] == "snapshots":
			return Annotation{Label: "snapshots containerd", Cleanup: cr.cleanup}, true
		case rest[1] != "snapshots" || !isDigits(rest[2]):
			return Annotation{}, false
		case len(rest) == 3:
			return Annotation{Label: "snapshot containerd", Cleanup: cr.cleanup}, true
		case len(rest) == 4 && (rest[3] == "fs" || rest[3] == "work"):
			return Annotation{Label: "snapshot containerd", Cleanup: cr.cleanup}, true
		}
		return Annotation{}, false
	}
	return Annotation{}, false
}

// ===========================================================================
// Kubernetes (kubelet)
// ===========================================================================

const (
	kubeletPods = "/var/lib/kubelet/pods"
	podLogs     = "/var/log/pods"
)

// podRef is a pod's namespace and name, sanitised for display (label) and
// raw for a suggested command when safe.
type podRef struct {
	label string // "<ns>/<name>"
	ns    string
	name  string
	safe  bool // ns and name are valid Kubernetes names, usable in a command
}

// parsePodLogDir parses a /var/log/pods entry, "<ns>_<name>_<uid>".
// Namespaces and pod names cannot contain '_', UIDs neither.
func parsePodLogDir(dir string) (ref podRef, uid string, ok bool) {
	parts := strings.Split(dir, "_")
	if len(parts) < 3 {
		return podRef{}, "", false
	}
	ns, uid := parts[0], parts[len(parts)-1]
	name := strings.Join(parts[1:len(parts)-1], "_")
	if ns == "" || name == "" || uid == "" {
		return podRef{}, "", false
	}
	return podRef{
		label: displayName(ns) + "/" + displayName(name),
		ns:    ns,
		name:  name,
		safe:  isKubeName(ns) && isKubeName(name),
	}, uid, true
}

// isKubeName reports whether s is a valid DNS-1123 subdomain (namespace,
// pod name).
func isKubeName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '-' || c == '.') && i > 0 && i < len(s)-1:
		default:
			return false
		}
	}
	return true
}

// isPodUID accepts kubelet pod UIDs: UUIDs, or hex hashes for static pods.
func isPodUID(s string) bool {
	if len(s) < 8 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c == '-') {
			return false
		}
	}
	return true
}

// shortUID shortens a pod UID to its first UUID group (8 hex digits).
func shortUID(uid string) string {
	if i := strings.IndexByte(uid, '-'); i > 0 {
		return uid[:i]
	}
	if len(uid) > 8 {
		return uid[:8]
	}
	return uid
}

// podByUID resolves a pod UID through the /var/log/pods directory names
// (listed once).
func (a *Annotator) podByUID(uid string) (podRef, bool) {
	a.podsOnce.Do(func() {
		names := a.listNames(podLogs)
		a.pods = make(map[string]podRef, len(names))
		for _, n := range names {
			if ref, uid, ok := parsePodLogDir(n); ok {
				a.pods[uid] = ref
			}
		}
	})
	ref, ok := a.pods[uid]
	return ref, ok
}

func (a *Annotator) matchKubernetes(q *query) (Annotation, bool) {
	if !q.isDir {
		return Annotation{}, false
	}
	if rest, ok := under(q.path, kubeletPods); ok {
		switch {
		case len(rest) == 0:
			return Annotation{Label: "pods Kubernetes (kubelet)"}, true
		case !isPodUID(rest[0]):
			return Annotation{}, false
		case len(rest) == 1:
			if ref, ok := a.podByUID(rest[0]); ok {
				return Annotation{Label: "pod " + ref.label}, true
			}
			return Annotation{Label: "pod kubernetes " + shortUID(rest[0])}, true
		case len(rest) == 4 && rest[1] == "volumes" && rest[2] == "kubernetes.io~empty-dir":
			return a.emptyDir(rest[0], rest[3]), true
		}
		return Annotation{}, false
	}
	if rest, ok := under(q.path, podLogs); ok {
		switch len(rest) {
		case 0:
			return Annotation{Label: "logs des pods Kubernetes", Cleanup: cleanPodLogs}, true
		case 1:
			if ref, _, ok := parsePodLogDir(rest[0]); ok {
				return Annotation{Label: "logs du pod " + ref.label, Cleanup: cleanPodLogs}, true
			}
		}
	}
	return Annotation{}, false
}

// emptyDir labels an emptyDir volume, a frequent disk-full culprit: it lives
// on the node's disk and is freed only when the pod goes away.
func (a *Annotator) emptyDir(uid, vol string) Annotation {
	ref, ok := a.podByUID(uid)
	if !ok {
		return Annotation{
			Label:   "emptyDir " + displayName(vol) + " du pod " + shortUID(uid),
			Cleanup: "libéré à la suppression du pod",
		}
	}
	ann := Annotation{
		Label:   "emptyDir " + displayName(vol) + " du pod " + ref.label,
		Cleanup: "libéré à la suppression du pod",
	}
	if ref.safe {
		ann.Cleanup += " : kubectl -n " + ref.ns + " delete pod " + ref.name
	}
	return ann
}
