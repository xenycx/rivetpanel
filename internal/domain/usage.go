package domain

// Usage analytics bucket lengths in seconds (bot_usage.res / node_usage.res).
const (
	UsageRes5m  = 300
	UsageRes1h  = 3600
	UsageRes1d  = 86400
	UsageResMin = UsageRes5m
)

// UsageBucket is one bot_usage row: sums and counts for a bot over one
// bucket (see migration 0052 for the meaning of each column).
type UsageBucket struct {
	BotID         string
	Res           int64
	BucketMS      int64
	Samples       int64
	Wanted        int64
	Up            int64
	Measured      int64
	CPUSum        float64
	CPUMax        float64
	MemSum        int64
	MemMax        int64
	MemLimit      int64
	NetRx         int64
	NetTx         int64
	DiskMax       *int64
	Crashes       int64
	Starts        int64
	DeploysOK     int64
	DeploysFailed int64
	DeployMS      int64
	BackupsOK     int64
	BackupsFailed int64
	BackupBytes   int64
}

// Add folds another bucket's sums into b (used for totals).
func (b *UsageBucket) Add(o UsageBucket) {
	b.Samples += o.Samples
	b.Wanted += o.Wanted
	b.Up += o.Up
	b.Measured += o.Measured
	b.CPUSum += o.CPUSum
	b.CPUMax = max(b.CPUMax, o.CPUMax)
	b.MemSum += o.MemSum
	b.MemMax = max(b.MemMax, o.MemMax)
	b.MemLimit = max(b.MemLimit, o.MemLimit)
	b.NetRx += o.NetRx
	b.NetTx += o.NetTx
	if o.DiskMax != nil && (b.DiskMax == nil || *o.DiskMax > *b.DiskMax) {
		v := *o.DiskMax
		b.DiskMax = &v
	}
	b.Crashes += o.Crashes
	b.Starts += o.Starts
	b.DeploysOK += o.DeploysOK
	b.DeploysFailed += o.DeploysFailed
	b.DeployMS += o.DeployMS
	b.BackupsOK += o.BackupsOK
	b.BackupsFailed += o.BackupsFailed
	b.BackupBytes += o.BackupBytes
}

// UsageSubject is what the usage collector needs to know about a bot.
type UsageSubject struct {
	ID              string
	NodeID          string
	DesiredState    string
	ObservedState   string
	ContainerID     *string
	RestartCount    int64
	LastStartedAtMS *int64
	MemoryBytes     int64
}

// NodeUsageBucket is one node_usage row.
type NodeUsageBucket struct {
	NodeID     string
	Res        int64
	BucketMS   int64
	Samples    int64
	CPUSum     float64
	CPUMax     float64
	MemSum     int64
	MemMax     int64
	MemTotal   int64
	DiskUsed   int64
	DiskTotal  int64
	NetRxSum   int64
	NetTxSum   int64
	RunningMax int64
}

// UsageAggregate is one bucket summed over many bots (admin overview):
// CPU and memory are the sum of each bot's average in the bucket.
type UsageAggregate struct {
	BucketMS      int64
	CPUCores      float64
	MemBytes      int64
	NetRx         int64
	NetTx         int64
	Wanted        int64
	Up            int64
	Crashes       int64
	Starts        int64
	DeploysOK     int64
	DeploysFailed int64
	DeployMS      int64
	BackupsOK     int64
	BackupsFailed int64
	BackupBytes   int64
}

// UsageConsumer is a bot's averages over a range (top consumers).
type UsageConsumer struct {
	BotID    string
	Name     string
	Kind     string
	OwnerID  string
	NodeID   string
	CPUCores float64
	MemBytes int64
	NetBytes int64
}

// UsageFilter limits panel-wide usage queries to bots whose owner is not in
// ExcludeOwners (nil = every bot).
type UsageFilter struct {
	ExcludeOwners []string
}

// TicketStat is the part of a support ticket the overview needs.
type TicketStat struct {
	UserID          string
	CreatedAtMS     int64
	ClosedAtMS      *int64
	Status          string
	FirstResponseMS *int64 // first staff reply visible to the requester
}
