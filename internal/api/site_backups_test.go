package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type siteBackupsOut struct {
	Backups []siteBackupDTO `json:"backups"`
	Health  struct {
		Available bool  `json:"available"`
		Enabled   bool  `json:"enabled"`
		UpToDate  bool  `json:"up_to_date"`
		NextDueMS int64 `json:"next_due_ms"`
		Count     int   `json:"count"`
	} `json:"health"`
}

// Files sites are archived on the backup schedule unless switched off, skip
// unchanged releases, keep manual backups, restore as a new release and lose
// their archives with the site.
func TestFilesSiteBackups(t *testing.T) {
	r := newSiteRig(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	r.svc.Now = func() time.Time { return now }
	r.svc.BackupDir = filepath.Join(t.TempDir(), "backups")
	r.svc.BackupInterval = time.Hour
	r.svc.BackupKeep = 2
	c := r.owner
	var site struct{ ID string }
	json.Unmarshal(c.mustStatus(201, "POST", "/api/v1/sites", map[string]string{"name": "Kept", "slug": "kept"}), &site)
	base := "/api/v1/sites/" + site.ID
	list := func() siteBackupsOut {
		var out siteBackupsOut
		json.Unmarshal(c.mustStatus(200, "GET", base+"/backups", nil), &out)
		return out
	}
	publish := func(body string) {
		t.Helper()
		if resp, b := c.raw("POST", base+"/upload", zipOf(t, map[string]string{"index.html": body}), "application/zip"); resp.StatusCode != 201 {
			t.Fatalf("upload: %d %s", resp.StatusCode, b)
		}
	}
	ctx := context.Background()

	// Nothing published: nothing to back up, nothing due.
	if l := list(); !l.Health.Available || !l.Health.Enabled || l.Health.NextDueMS != 0 {
		t.Fatalf("empty site health %+v", l.Health)
	}
	c.mustStatus(400, "POST", base+"/backups", nil)

	publish("v1")
	if l := list(); l.Health.NextDueMS == 0 || l.Health.UpToDate {
		t.Fatalf("published site health %+v", l.Health)
	}
	r.svc.RunScheduledBackups(ctx)
	r.svc.RunScheduledBackups(ctx) // the same release is not archived twice
	if l := list(); len(l.Backups) != 1 || l.Backups[0].Kind != "auto" || !l.Health.UpToDate || l.Health.NextDueMS != 0 {
		t.Fatalf("after the schedule: %+v", l)
	}
	first := list().Backups[0]

	// A new release within the interval waits; after it, it is archived.
	publish("v2")
	r.svc.RunScheduledBackups(ctx)
	if n := len(list().Backups); n != 1 {
		t.Fatalf("backed up before the interval: %d", n)
	}
	now = now.Add(2 * time.Hour)
	r.svc.RunScheduledBackups(ctx)
	if n := len(list().Backups); n != 2 {
		t.Fatalf("not backed up after the interval: %d", n)
	}

	// Switched off: no more automatic backups; manual ones still work.
	var dto siteDTO
	json.Unmarshal(c.mustStatus(200, "PUT", base+"/auto-backup", map[string]bool{"enabled": false}), &dto)
	if dto.AutoBackup {
		t.Fatal("switch did not turn off")
	}
	publish("v3")
	now = now.Add(2 * time.Hour)
	r.svc.RunScheduledBackups(ctx)
	if l := list(); len(l.Backups) != 2 || l.Health.Enabled || l.Health.NextDueMS != 0 {
		t.Fatalf("switched off: %+v", l)
	}
	var manual siteBackupDTO
	json.Unmarshal(c.mustStatus(201, "POST", base+"/backups", map[string]string{"label": "before redesign"}), &manual)
	if manual.Kind != "manual" || manual.Label == nil || *manual.Label != "before redesign" || manual.Files != 1 {
		t.Fatalf("manual backup %+v", manual)
	}

	// The download is the release as a tar.gz.
	resp, body := c.do("GET", base+"/backups/"+manual.ID+"/download", nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "Kept-") {
		t.Fatalf("download: %d %v", resp.StatusCode, resp.Header)
	}
	if got := tarFile(t, body, "index.html"); got != "v3" {
		t.Fatalf("archived index.html = %q", got)
	}

	// Restoring the first backup serves v1 again as a new release.
	c.mustStatus(201, "POST", base+"/backups/"+first.ID+"/restore", nil)
	if rec := r.get("kept.sites.test:8081", "/"); rec.Code != 200 || rec.Body.String() != "v1" {
		t.Fatalf("after restore: %d %q", rec.Code, rec.Body.String())
	}

	// Retention keeps the newest two automatic backups.
	c.mustStatus(200, "PUT", base+"/auto-backup", map[string]bool{"enabled": true})
	publish("v4")
	now = now.Add(2 * time.Hour)
	r.svc.RunScheduledBackups(ctx)
	autos := 0
	for _, b := range list().Backups {
		if b.Kind == "auto" {
			autos++
			if b.ID == first.ID {
				t.Fatal("the oldest automatic backup was kept beyond the retention")
			}
		}
	}
	if autos != 2 {
		t.Fatalf("automatic backups after retention = %d, want 2", autos)
	}

	// Deleting a backup removes its file.
	c.mustStatus(204, "DELETE", base+"/backups/"+manual.ID, nil)
	dir := filepath.Join(r.svc.BackupDir, "sites", site.ID)
	if _, err := os.Stat(filepath.Join(dir, manual.ID+".tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("deleted backup file still there: %v", err)
	}
	c.mustStatus(204, "DELETE", base, nil)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("site backups outlived the site: %v", err)
	}
}

func tarFile(t *testing.T, gz []byte, name string) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("%s not in the archive: %v", name, err)
		}
		if strings.TrimPrefix(h.Name, "./") == name {
			b, _ := io.ReadAll(tr)
			return string(b)
		}
	}
}
