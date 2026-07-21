// backup-viewer: a tiny read-mostly UI for the backup2 CronJob.
//
//   GET  /      last run's logs (live from the Kubernetes API) + list of backups on Google Drive
//   POST /run   create a Job from cronjob/backup ("Run backup now")
//
// It shells out to kubectl (in-cluster ServiceAccount) and rclone (config via env). No secrets or
// state of its own. Auth is handled in front by a Traefik middleware on the Ingress.
package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Namespace and CronJob name are injected by the chart so the viewer works regardless of how the
// resources are named at install time.
var (
	namespace   = envOr("NAMESPACE", "default")
	cronjobName = envOr("CRONJOB_NAME", "backup")
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func remote() string { return envOr("RCLONE_REMOTE", "gdrive:") }

// run executes a command and returns stdout; on failure the error carries stderr.
func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("%s", msg)
	}
	return out.String(), nil
}

type pageData struct {
	Now       string
	Ran       string
	Err       string
	JobName   string
	JobStatus string
	Logs      string
	Backups   []backupFile
}

type backupFile struct {
	Name     string
	Size     string
	Modified string
}

// latestJob returns the most recent backup-* Job's name and a coarse status.
func latestJob() (name, status string, err error) {
	out, err := run("kubectl", "get", "jobs", "-n", namespace, "-o", "json")
	if err != nil {
		return "", "", err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Status struct {
				Active    int `json:"active"`
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return "", "", err
	}
	var newest int = -1
	for i, it := range list.Items {
		if !strings.HasPrefix(it.Metadata.Name, "backup") {
			continue
		}
		if newest == -1 || it.Metadata.CreationTimestamp.After(list.Items[newest].Metadata.CreationTimestamp) {
			newest = i
		}
	}
	if newest == -1 {
		return "", "", nil
	}
	j := list.Items[newest]
	switch {
	case j.Status.Active > 0:
		status = "running"
	case j.Status.Failed > 0:
		status = "failed"
	case j.Status.Succeeded > 0:
		status = "succeeded"
	default:
		status = "pending"
	}
	return j.Metadata.Name, status, nil
}

func jobLogs(name string) string {
	if name == "" {
		return "no backup jobs found yet."
	}
	out, err := run("kubectl", "logs", "-n", namespace, "job/"+name, "--tail=2000")
	if err != nil {
		return "could not read logs: " + err.Error()
	}
	if strings.TrimSpace(out) == "" {
		return "(no output yet)"
	}
	return out
}

func listBackups() ([]backupFile, error) {
	out, err := run("rclone", "lsjson", remote())
	if err != nil {
		return nil, err
	}
	var items []struct {
		Name    string    `json:"Name"`
		Size    int64     `json:"Size"`
		ModTime time.Time `json:"ModTime"`
		IsDir   bool      `json:"IsDir"`
	}
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ModTime.After(items[j].ModTime) })
	var files []backupFile
	for _, it := range items {
		if it.IsDir {
			continue
		}
		files = append(files, backupFile{
			Name:     it.Name,
			Size:     humanSize(it.Size),
			Modified: it.ModTime.UTC().Format("2006-01-02 15:04"),
		})
	}
	return files, nil
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data := pageData{Now: time.Now().UTC().Format("2006-01-02 15:04:05 UTC"), Ran: r.URL.Query().Get("ran")}
	name, status, err := latestJob()
	if err != nil {
		data.Err = "kubectl: " + err.Error()
	}
	data.JobName, data.JobStatus = name, status
	data.Logs = jobLogs(name)
	backups, err := listBackups()
	if err != nil && data.Err == "" {
		data.Err = "rclone: " + err.Error()
	}
	data.Backups = backups
	if err := page.Execute(w, data); err != nil {
		log.Printf("template: %v", err)
	}
}

func handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := "backup-manual-" + time.Now().UTC().Format("20060102-150405")
	if _, err := run("kubectl", "create", "job", "-n", namespace, name, "--from=cronjob/"+cronjobName); err != nil {
		http.Error(w, "failed to start backup: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/?ran="+name, http.StatusSeeOther)
}

func handleHealth(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") }

func main() {
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/run", handleRun)
	http.HandleFunc("/healthz", handleHealth)
	addr := ":8080"
	log.Printf("backup-viewer listening on %s (remote=%s)", addr, remote())
	log.Fatal(http.ListenAndServe(addr, nil))
}

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="20">
<title>backup2</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body { font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif; margin: 0; padding: 1.5rem;
         max-width: 1000px; margin-inline: auto; line-height: 1.5; }
  header { display: flex; align-items: baseline; justify-content: space-between; gap: 1rem;
           border-bottom: 2px solid currentColor; padding-bottom: .5rem; margin-bottom: 1.25rem; }
  h1 { margin: 0; font-size: 1.5rem; letter-spacing: .02em; }
  h2 { font-size: 1.05rem; margin: 0; }
  .now { opacity: .6; font-variant-numeric: tabular-nums; font-size: .85rem; }
  .bar { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-bottom: .5rem; }
  section { margin-bottom: 1.75rem; }
  button { font: inherit; font-weight: 600; padding: .5rem .9rem; border: 0; border-radius: 6px;
           background: #2563eb; color: #fff; cursor: pointer; }
  button:hover { background: #1d4ed8; }
  table { width: 100%; border-collapse: collapse; font-size: .9rem; }
  th, td { text-align: left; padding: .4rem .6rem; border-bottom: 1px solid rgba(128,128,128,.3); }
  th { font-weight: 600; opacity: .8; }
  td:nth-child(2), th:nth-child(2) { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  td:nth-child(3), th:nth-child(3) { white-space: nowrap; font-variant-numeric: tabular-nums; }
  .status { font-size: .8rem; font-weight: 700; padding: .1rem .5rem; border-radius: 999px; vertical-align: middle; }
  .status.running { background: #f59e0b; color: #000; }
  .status.succeeded { background: #16a34a; color: #fff; }
  .status.failed { background: #dc2626; color: #fff; }
  .status.pending { background: #6b7280; color: #fff; }
  .terminal { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .8rem;
              background: #0b0e14; color: #d6deeb; padding: 1rem; border-radius: 8px;
              white-space: pre-wrap; word-break: break-word; max-height: 55vh; overflow: auto; }
  .banner { background: rgba(37,99,235,.15); border: 1px solid #2563eb; padding: .6rem .9rem;
            border-radius: 6px; margin-bottom: 1rem; }
  .err { background: rgba(220,38,38,.15); border: 1px solid #dc2626; padding: .6rem .9rem;
         border-radius: 6px; margin-bottom: 1rem; }
</style>
</head>
<body>
<header><h1>backup2</h1><span class="now">{{.Now}}</span></header>
{{if .Ran}}<div class="banner">Started <strong>{{.Ran}}</strong> — logs will appear below shortly.</div>{{end}}
{{if .Err}}<div class="err">{{.Err}}</div>{{end}}
<section>
  <div class="bar">
    <h2>Backups on Drive</h2>
    <form method="post" action="run" onsubmit="this.querySelector('button').disabled=true;this.querySelector('button').textContent='Starting…'">
      <button type="submit">Run backup now</button>
    </form>
  </div>
  <table>
    <thead><tr><th>Name</th><th>Size</th><th>Modified (UTC)</th></tr></thead>
    <tbody>
    {{range .Backups}}<tr><td>{{.Name}}</td><td>{{.Size}}</td><td>{{.Modified}}</td></tr>
    {{else}}<tr><td colspan="3">no backups found</td></tr>{{end}}
    </tbody>
  </table>
</section>
<section>
  <div class="bar">
    <h2>Last run{{if .JobName}} — {{.JobName}}{{end}}</h2>
    {{if .JobStatus}}<span class="status {{.JobStatus}}">{{.JobStatus}}</span>{{end}}
  </div>
  <pre class="terminal">{{.Logs}}</pre>
</section>
</body>
</html>
`))
