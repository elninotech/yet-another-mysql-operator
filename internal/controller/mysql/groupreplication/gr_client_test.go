package groupreplication

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"testing"
)

// These tests exercise the SQL helpers with a stub driver instead of a real MySQL instance.

func TestAnyOnlineMember(t *testing.T) {
	ctx := context.Background()
	db := newStubDB(stubConfig{onlineMember: true})

	got, err := anyOnlineMember(ctx, db)
	if err != nil {
		t.Fatalf("anyOnlineMember: %v", err)
	}
	if !got {
		t.Fatalf("expected true when stub reports an online member")
	}
}

func TestAnyOnlineMemberNone(t *testing.T) {
	ctx := context.Background()
	db := newStubDB(stubConfig{onlineMember: false})

	got, err := anyOnlineMember(ctx, db)
	if err != nil {
		t.Fatalf("anyOnlineMember: %v", err)
	}
	if got {
		t.Fatalf("expected false when no members are online")
	}
}

func TestInGroup(t *testing.T) {
	ctx := context.Background()
	db := newStubDB(stubConfig{inGroupState: "ONLINE"})

	got, err := inGroup(ctx, db)
	if err != nil {
		t.Fatalf("inGroup: %v", err)
	}
	if !got {
		t.Fatalf("expected true when MEMBER_STATE is ONLINE")
	}
}

func TestInGroupNone(t *testing.T) {
	ctx := context.Background()
	db := newStubDB(stubConfig{})

	got, err := inGroup(ctx, db)
	if err != nil {
		t.Fatalf("inGroup: %v", err)
	}
	if got {
		t.Fatalf("expected false when no rows are returned")
	}
}

func TestGTIDSets(t *testing.T) {
	ctx := context.Background()
	db := newStubDB(stubConfig{
		gtidExecuted:  "uuid:1",
		gtidCertified: "uuid:2",
	})
	client := &Client{db: db}

	sets, err := client.GTIDSets(ctx)
	if err != nil {
		t.Fatalf("GTIDSets: %v", err)
	}
	if sets.Executed != "uuid:1" || sets.Certified != "uuid:2" {
		t.Fatalf("unexpected GTID sets: %+v", sets)
	}
}

func TestSuperset(t *testing.T) {
	ctx := context.Background()
	db := newStubDB(stubConfig{
		subtract: map[string]string{
			"subset||superset": "",
			"superset||subset": "1",
		},
	})

	a := GTIDSets{Executed: "superset", Certified: "superset"}
	b := GTIDSets{Executed: "subset", Certified: "subset"}

	ok, err := superset(ctx, db, a, b)
	if err != nil {
		t.Fatalf("superset a>b: %v", err)
	}
	if !ok {
		t.Fatalf("expected a to be superset of b")
	}

	ok, err = superset(ctx, db, b, a)
	if err != nil {
		t.Fatalf("superset b>a: %v", err)
	}
	if ok {
		t.Fatalf("expected b not to be superset of a")
	}
}

func TestPickRestartCandidate(t *testing.T) {
	ctx := context.Background()
	db := newStubDB(stubConfig{
		subtract: map[string]string{
			"gtid-b||gtid-a": "",
			"gtid-a||gtid-b": "1",
		},
	})

	infos := []GTIDInfo{
		{Pod: "a", Ordinal: 1, Sets: GTIDSets{Executed: "gtid-a", Certified: "gtid-a"}},
		{Pod: "b", Ordinal: 0, Sets: GTIDSets{Executed: "gtid-b", Certified: "gtid-b"}},
	}

	pod, err := PickRestartCandidate(ctx, db, infos)
	if err != nil {
		t.Fatalf("PickRestartCandidate: %v", err)
	}
	if pod != "a" {
		t.Fatalf("expected pod a to be chosen, got %s", pod)
	}
}

// --- stub driver helpers ---

type stubConfig struct {
	onlineMember   bool
	inGroupState   string
	gtidExecuted   string
	gtidCertified  string
	subtract       map[string]string
	replUserExists bool
}

type stubDriver struct {
	cfg stubConfig
}

func (d *stubDriver) Open(string) (driver.Conn, error) { return &stubConn{cfg: d.cfg}, nil }

type stubConnector struct {
	drv *stubDriver
}

func (c *stubConnector) Connect(context.Context) (driver.Conn, error) {
	return &stubConn{cfg: c.drv.cfg}, nil
}

func (c *stubConnector) Driver() driver.Driver { return c.drv }

type stubConn struct {
	cfg stubConfig
}

func (c *stubConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("prepare not supported")
}
func (c *stubConn) Close() error              { return nil }
func (c *stubConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("tx not supported") }

func (c *stubConn) Ping(context.Context) error { return nil }

func (c *stubConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return stubResult{}, nil
}

func (c *stubConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "replication_group_members") && strings.Contains(query, "MEMBER_STATE = 'ONLINE'"):
		if c.cfg.onlineMember {
			return newRows([][]driver.Value{{"ONLINE"}}), nil
		}
		return newRows(nil), nil
	case strings.Contains(query, "replication_group_members") && strings.Contains(query, "@@server_uuid"):
		if c.cfg.inGroupState != "" {
			return newRows([][]driver.Value{{c.cfg.inGroupState}}), nil
		}
		return newRows(nil), nil
	case strings.Contains(strings.ToLower(query), "from mysql.user"):
		if c.cfg.replUserExists {
			return newRows([][]driver.Value{{int64(1)}}), nil
		}
		return newRows(nil), nil
	case strings.Contains(query, "@@GLOBAL.GTID_EXECUTED"):
		return newRows([][]driver.Value{{c.cfg.gtidExecuted}}), nil
	case strings.Contains(query, "replication_connection_status"):
		if c.cfg.gtidCertified != "" {
			return newRows([][]driver.Value{{c.cfg.gtidCertified}}), nil
		}
		return newRows(nil), nil
	case strings.Contains(query, "GTID_SUBTRACT"):
		key := ""
		if len(args) == 2 {
			key = fmt.Sprintf("%v||%v", args[0].Value, args[1].Value)
		}
		return newRows([][]driver.Value{{c.cfg.subtract[key]}}), nil
	case strings.Contains(query, "SELECT 1"):
		return newRows([][]driver.Value{{int64(1)}}), nil
	default:
		return newRows(nil), nil
	}
}

type stubResult struct{}

func (stubResult) LastInsertId() (int64, error) { return 0, nil }
func (stubResult) RowsAffected() (int64, error) { return 0, nil }

type stubRows struct {
	cols []string
	data [][]driver.Value
	idx  int
}

func newRows(data [][]driver.Value) *stubRows {
	return &stubRows{cols: []string{"c"}, data: data, idx: 0}
}

func (r *stubRows) Columns() []string { return r.cols }

func (r *stubRows) Close() error { return nil }

func (r *stubRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.idx])
	r.idx++
	return nil
}

func newStubDB(cfg stubConfig) *sql.DB {
	drv := &stubDriver{cfg: cfg}
	return sql.OpenDB(&stubConnector{drv: drv})
}

func TestEnsureReplicationUserSkipsWhenExists(t *testing.T) {
	ctx := context.Background()
	db := newStubDB(stubConfig{replUserExists: true})

	if err := ensureReplicationUser(ctx, db, "pw"); err != nil {
		t.Fatalf("ensureReplicationUser (existing user) returned error: %v", err)
	}
}
