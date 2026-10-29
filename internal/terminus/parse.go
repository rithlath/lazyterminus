package terminus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// KeyedRow pairs a row with the object key Terminus listed it under.
type KeyedRow[T any] struct {
	Key string
	Row T
}

// DecodeRows decodes Terminus list output, which is a JSON object keyed by ID
// when there are rows and a JSON array (usually `[]`) otherwise. Object order
// is preserved. Leading non-JSON noise (stray notices) is skipped.
func DecodeRows[T any](data []byte) ([]KeyedRow[T], error) {
	if string(bytes.TrimSpace(data)) == "null" {
		return nil, nil
	}
	data, err := jsonStart(data)
	if err != nil {
		return nil, err
	}
	if data[0] == '[' {
		var rows []T
		if err := json.Unmarshal(data, &rows); err != nil {
			return nil, err
		}
		out := make([]KeyedRow[T], len(rows))
		for i, r := range rows {
			out[i] = KeyedRow[T]{Key: fmt.Sprint(i), Row: r}
		}
		return out, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil { // {
		return nil, err
	}
	var out []KeyedRow[T]
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var row T
		if err := dec.Decode(&row); err != nil {
			return nil, fmt.Errorf("row %q: %w", key, err)
		}
		out = append(out, KeyedRow[T]{Key: key, Row: row})
	}
	return out, nil
}

func rowsOnly[T any](data []byte) ([]T, error) {
	kr, err := DecodeRows[T](data)
	if err != nil {
		return nil, err
	}
	out := make([]T, len(kr))
	for i, r := range kr {
		out[i] = r.Row
	}
	return out, nil
}

// jsonStart trims anything before the JSON document, such as a stray
// "[notice]" line (which itself starts with '['), and anything after it.
func jsonStart(data []byte) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("empty output from terminus")
	}
	for off := 0; off < len(data); {
		i := bytes.IndexAny(data[off:], "{[")
		if i < 0 {
			break
		}
		var raw json.RawMessage
		if err := json.NewDecoder(bytes.NewReader(data[off+i:])).Decode(&raw); err == nil {
			return raw, nil
		}
		off += i + 1
	}
	return nil, fmt.Errorf("no JSON in terminus output: %.80q", data)
}

// ParseSites parses site:list output, sorted by name.
func ParseSites(data []byte) ([]Site, error) {
	sites, err := rowsOnly[Site](data)
	sort.Slice(sites, func(i, j int) bool { return sites[i].Name < sites[j].Name })
	return sites, err
}

// ParseSite parses site:info output.
func ParseSite(data []byte) (Site, error) {
	var s Site
	data, err := jsonStart(data)
	if err == nil {
		err = json.Unmarshal(data, &s)
	}
	return s, err
}

// ParseEnvs parses env:list output and orders it dev, test, live, then
// multidevs by name.
func ParseEnvs(data []byte) ([]Env, error) {
	envs, err := rowsOnly[Env](data)
	SortEnvs(envs)
	return envs, err
}

// SortEnvs orders environments dev, test, live, then multidevs by name.
func SortEnvs(envs []Env) {
	rank := func(id string) int {
		switch id {
		case "dev":
			return 0
		case "test":
			return 1
		case "live":
			return 2
		}
		return 3
	}
	sort.SliceStable(envs, func(i, j int) bool {
		ri, rj := rank(envs[i].ID), rank(envs[j].ID)
		if ri != rj {
			return ri < rj
		}
		return envs[i].ID < envs[j].ID
	})
}

// ParseEnv parses env:info output.
func ParseEnv(data []byte) (Env, error) {
	var e Env
	data, err := jsonStart(data)
	if err == nil {
		err = json.Unmarshal(data, &e)
	}
	return e, err
}

// ParseMultidevs parses multidev:list output, sorted by name.
func ParseMultidevs(data []byte) ([]Multidev, error) {
	mds, err := rowsOnly[Multidev](data)
	sort.Slice(mds, func(i, j int) bool { return mds[i].ID < mds[j].ID })
	return mds, err
}

// ParseCodeLog parses env:code-log output (newest first, as Terminus emits).
func ParseCodeLog(data []byte) ([]Commit, error) { return rowsOnly[Commit](data) }

// ParseDiffstat parses env:diffstat output.
func ParseDiffstat(data []byte) ([]DiffstatEntry, error) {
	kr, err := DecodeRows[DiffstatEntry](data)
	if err != nil {
		return nil, err
	}
	out := make([]DiffstatEntry, len(kr))
	for i, r := range kr {
		out[i] = r.Row
		if out[i].File == "" { // keyed by filename in some Terminus versions
			out[i].File = r.Key
		}
	}
	return out, nil
}

// ParseWorkflows parses workflow:list output, newest first.
func ParseWorkflows(data []byte) ([]Workflow, error) {
	wfs, err := rowsOnly[Workflow](data)
	sort.SliceStable(wfs, func(i, j int) bool { return wfs[i].StartedAt > wfs[j].StartedAt })
	return wfs, err
}

// ParseWorkflow parses workflow:info:status output.
func ParseWorkflow(data []byte) (Workflow, error) {
	var w Workflow
	data, err := jsonStart(data)
	if err == nil {
		err = json.Unmarshal(data, &w)
	}
	return w, err
}

// ParseWorkflowOps parses workflow:info:operations output in order.
func ParseWorkflowOps(data []byte) ([]WorkflowOp, error) {
	kr, err := DecodeRows[WorkflowOp](data)
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowOp, len(kr))
	for i, r := range kr {
		out[i] = r.Row
		out[i].ID = r.Key
	}
	return out, nil
}

// ParseBackups parses backup:list output, newest first.
func ParseBackups(data []byte) ([]Backup, error) {
	bs, err := rowsOnly[Backup](data)
	sort.SliceStable(bs, func(i, j int) bool { return bs[i].Date > bs[j].Date })
	return bs, err
}

// ParseUpstreamUpdates parses upstream:updates:list output.
func ParseUpstreamUpdates(data []byte) ([]UpstreamUpdate, error) {
	return rowsOnly[UpstreamUpdate](data)
}

// ParseDomains parses domain:list output.
func ParseDomains(data []byte) ([]Domain, error) { return rowsOnly[Domain](data) }
