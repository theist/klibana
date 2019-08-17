package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/joho/godotenv"
)

type config struct {
	elasticsearch struct {
		host string
		port string
	}
	query struct {
		queryTemplate string
		queryHeaders  []string
		queryRows     string
		queryDebug    bool
		timeMode      string
		gte           int64
		lte           int64
	}
	debug bool
}

func printTimeHelp() {
	fmt.Println(`klibana --time-window help

Possible values:

   today         Uses records from midnight to now
   yesterday     Uses records from yesterday at 0:00 to today at 0:00
   week          Uses recrods from last Sunday at 0:00 to now
   month         Uses records from day 1 of this month at 0:00 to now

All times are local, and weeks starts on Sunday`)
}

func (c *config) timeWindowFill() error {
	now := time.Now()

	switch c.query.timeMode {
	case "":
		c.query.lte = 0
		c.query.gte = 0
	case "help":
		printTimeHelp()
		os.Exit(0)
	case "today":
		c.query.lte = now.Unix()
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		c.query.gte = start.Unix()
	case "yesterday":
		end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		c.query.lte = end.Unix()
		c.query.gte = c.query.lte - (24 * 60 * 60)
	case "week":
		c.query.lte = now.Unix()
		todayAtMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		daysPassed := now.Weekday()
		c.query.gte = todayAtMidnight.Unix() - (int64(daysPassed) * 24 * 60 * 60)
	case "month":
		c.query.lte = now.Unix()
		firstDayOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		c.query.gte = firstDayOfMonth.Unix()
	default:
		return fmt.Errorf("Unknown time mode %v", c.query.timeMode)
	}
	return nil
}

func (c *config) load() error {
	var queryFile string
	var host, port string

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	_ = godotenv.Load()
	_ = godotenv.Load(path.Join(homeDir, ".klibanarc"))

	flag.StringVar(&queryFile, "query-file", "", "File containing the elastic query and the processing instructions")
	flag.BoolVar(&c.query.queryDebug, "es-result", false, "Stop and dump after getting ES result")
	flag.StringVar(&c.query.timeMode, "time-window", "", "Predefined time windows, use --time-window help to see available settings")
	flag.BoolVar(&c.debug, "debug", false, "Turns on debug log")
	flag.StringVar(&host, "host", "", "Host for connecting elasticsearch")
	flag.StringVar(&port, "port", "", "Port for connecting elasticsearch")

	flag.Parse()
	if host != "" {
		os.Setenv("KLIBANA_HOST", host)
	}

	if port != "" {
		os.Setenv("KLIBANA_PORT", port)
	}

	c.elasticsearch.port = port
	c.elasticsearch.host = host

	if c.elasticsearch.port == "" {
		c.elasticsearch.port = "9200"
	}

	if c.elasticsearch.host == "" {
		c.elasticsearch.host = "localhost"
	}

	err = c.timeWindowFill()
	if err != nil {
		return err
	}
	// from here it needs a template file to read

	if queryFile == "" {
		return fmt.Errorf("Template file not specified please use --query-file with a valid query file")
	}

	jsonFile, err := os.Open(queryFile)
	if err != nil {
		return err
	}
	jsonBytes, err := ioutil.ReadAll(jsonFile)
	if err != nil {
		return err
	}

	qh := gjson.Get(string(jsonBytes), "query_headers")
	for _, item := range qh.Array() {
		c.query.queryHeaders = append(c.query.queryHeaders, item.String())
	}
	c.query.queryRows = gjson.Get(string(jsonBytes), "query_rows").String()
	q := gjson.Get(string(jsonBytes), "query_template").String()

	if strings.Contains(q, "###LTE###") {
		if c.query.lte == 0 {
			return fmt.Errorf("LTE present in template and --time-window not specified")
		}
		q = strings.ReplaceAll(q, "###LTE###", strconv.FormatInt(c.query.lte*1000, 10))
	}
	if strings.Contains(q, "###GTE###") {
		if c.query.lte == 0 {
			return fmt.Errorf("LTE present in template and --time-window not specified")
		}
		q = strings.ReplaceAll(q, "###GTE###", strconv.FormatInt(c.query.gte*1000, 10))
	}
	c.query.queryTemplate = q
	return nil
}

type row []string

type table []row

func (r *row) toCSV() string {
	return strings.Join(*r, ",")
}

func (t *table) toCSV() string {
	var res []string
	for _, r := range *t {
		res = append(res, r.toCSV())
	}
	return strings.Join(res, "\n")
}

func esQuery(host, port, query string) (string, error) {
	body := strings.NewReader(query)
	uri := fmt.Sprintf("http://%v:%v/logstash*/_search", host, port)
	req, err := http.NewRequest("POST", uri, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP error to ES call %v", resp.Status)
	}
	result, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return string(result), nil
}

func jqQuery(headers row, query string, data string) (table, error) {
	var tbl table
	tbl = append(tbl, headers)
	bytes := []byte(data)
	tmpFile, err := ioutil.TempFile(os.TempDir(), "klibana.*.json")
	if err != nil {
		return nil, err
	}
	_, err = tmpFile.Write(bytes)
	if err != nil {
		return nil, err
	}
	tmpFile.Close()

	cmd := exec.Command("jq", "-c", query, tmpFile.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	res := string(out)
	res = strings.ReplaceAll(res, "[", "")
	res = strings.ReplaceAll(res, "]", "")
	for _, line := range strings.Split(res, "\n") {
		tbl = append(tbl, strings.Split(line, res))
	}

	defer os.Remove(tmpFile.Name())
	return tbl, nil
}

func main() {
	var cfg config
	err := cfg.load()
	log.SetPrefix("[klibana] ")
	if err != nil {
		log.Fatalf("There was an error loading config: %v", err)
	}
	if !cfg.debug {
		log.SetOutput(ioutil.Discard)
	}
	log.Printf("Config Loaded")
	log.Printf("Time window set %v to %v", time.Unix(cfg.query.gte, 0), time.Unix(cfg.query.lte, 0))
	log.Printf("Querying elastic search at http://%v:%v", cfg.elasticsearch.host, cfg.elasticsearch.port)
	esRes, err := esQuery(cfg.elasticsearch.host, cfg.elasticsearch.port, cfg.query.queryTemplate)
	if err != nil {
		log.SetOutput(os.Stderr)
		log.Printf("Error querying ES at %v:%v: %v", cfg.elasticsearch.host, cfg.elasticsearch.port, err)
		os.Exit(1)
	}
	if cfg.query.queryDebug {
		log.Println("Stopping after getting ES results:")
		fmt.Printf("%v", esRes)
		os.Exit(0)
	}
	log.Printf("executing jq -c with filter %v", cfg.query.queryRows)
	result, err := jqQuery(cfg.query.queryHeaders, cfg.query.queryRows, esRes)
	if err != nil {
		log.SetOutput(os.Stderr)
		log.Printf("Error executing jq: %v", err)
	}
	fmt.Printf("%v", result.toCSV())
	log.Printf("Done")
}
