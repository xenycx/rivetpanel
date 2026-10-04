package api

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// Usage analytics: a bot's Analytics tab and Administration → Analytics.
// Both answer JSON, or CSV of the chart rows with ?format=csv.

func (s *panel) botUsage(c fiber.Ctx) error {
	rep, err := s.usage.BotUsage(c.Context(), currentUser(c), strings.Clone(c.Params("id")), c.Query("range", "24h"))
	if err != nil {
		return err
	}
	c.Set("Cache-Control", "no-store")
	if c.Query("format") != "csv" {
		return c.JSON(rep)
	}
	rows := [][]string{{"time", "cpu_cores_avg", "cpu_cores_max", "memory_bytes_avg", "memory_bytes_max", "memory_limit_bytes",
		"net_rx_bytes", "net_tx_bytes", "disk_bytes", "uptime_percent", "crashes", "starts"}}
	for _, p := range rep.Points {
		rows = append(rows, []string{csvTime(p.T), csvF(p.CPU), csvF(p.CPUMax), csvI(p.Mem), csvI(p.MemMax), strconv.FormatInt(p.MemLimit, 10),
			csvI(p.NetRx), csvI(p.NetTx), csvI(p.Disk), csvF(p.Uptime), strconv.FormatInt(p.Crashes, 10), strconv.FormatInt(p.Starts, 10)})
	}
	rows = append(rows, nil, []string{"time", "deploys_succeeded", "deploys_failed", "deploy_avg_ms", "backups_succeeded",
		"backups_failed", "backup_bytes"})
	for _, e := range rep.Events {
		rows = append(rows, []string{csvTime(e.T), strconv.FormatInt(e.DeploysOK, 10), strconv.FormatInt(e.DeploysFailed, 10),
			csvI(e.DeployAvgMS), strconv.FormatInt(e.BackupsOK, 10), strconv.FormatInt(e.BackupsFailed, 10), strconv.FormatInt(e.BackupBytes, 10)})
	}
	return sendCSV(c, "usage-"+rep.Range+".csv", rows)
}

func (s *panel) adminAnalytics(c fiber.Ctx) error {
	ov, err := s.usage.Overview(c.Context(), currentUser(c), c.Query("range", "7d"))
	if err != nil {
		return err
	}
	c.Set("Cache-Control", "no-store")
	if c.Query("format") != "csv" {
		return c.JSON(ov)
	}
	head := []string{"time", "cpu_cores", "memory_bytes", "net_rx_bytes", "net_tx_bytes", "uptime_percent", "crashes", "starts",
		"deploys_succeeded", "deploys_failed", "backups_succeeded", "backups_failed", "backup_bytes", "new_accounts"}
	if ov.Tickets != nil {
		head = append(head, "tickets_opened")
	}
	rows := [][]string{head}
	for _, p := range ov.Points {
		cpu := p.CPU
		r := []string{csvTime(p.T), csvF(&cpu), strconv.FormatInt(p.Mem, 10), strconv.FormatInt(p.NetRx, 10), strconv.FormatInt(p.NetTx, 10),
			csvF(p.Uptime), strconv.FormatInt(p.Crashes, 10), strconv.FormatInt(p.Starts, 10), strconv.FormatInt(p.DeploysOK, 10),
			strconv.FormatInt(p.DeploysFailed, 10), strconv.FormatInt(p.BackupsOK, 10), strconv.FormatInt(p.BackupsFailed, 10),
			strconv.FormatInt(p.BackupBytes, 10), strconv.FormatInt(p.NewUsers, 10)}
		if ov.Tickets != nil {
			r = append(r, csvI(p.Tickets))
		}
		rows = append(rows, r)
	}
	return sendCSV(c, "panel-usage-"+ov.Range+".csv", rows)
}

func sendCSV(c fiber.Ctx, name string, rows [][]string) error {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+name+`"`)
	return c.Send(buf.Bytes())
}

func csvTime(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }

func csvF(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 4, 64)
}

func csvI(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}
