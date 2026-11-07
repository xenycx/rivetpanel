package domain

// ContainerConfig is what a container app (KindContainer) adds to a bot row:
// how its image runs inside the panel's isolation policy. The image itself
// is Bot.ImageRef; the command is Bot.Entrypoint/Bot.Argv (both empty keeps
// the image's own entrypoint and command).
type ContainerConfig struct {
	// User is "" (the panel's container user, which owns the volumes) or a
	// numeric "uid:gid" other than root.
	User string
	// Writable are absolute paths given a small in-memory folder, for images
	// that write to their own filesystem (the root filesystem is read-only).
	Writable []string
	// Volumes are persistent folders kept in the app's files (and backups).
	Volumes []Volume
	// StackID and Service place the container in a stack: Service is its host
	// name on the stack's private network. Empty for a standalone container.
	StackID string
	Service string
	// DependsOn are services of the same stack that must be running before
	// this one starts.
	DependsOn []string
}

// Volume is a persistent folder: volumes/<Name> in the app's files, mounted
// at Path inside the container.
type Volume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Stack groups container apps imported from one Compose file. Its services
// reach each other by name on a private network without internet access;
// each service keeps its own outbound access setting.
type Stack struct {
	ID          string
	WorkspaceID string
	OwnerID     string
	Name        string
	// ComposeYAML is the file the stack was imported from, kept for
	// reference (at most 256 KiB).
	ComposeYAML string
	CreatedAtMS int64
	UpdatedAtMS int64
}
