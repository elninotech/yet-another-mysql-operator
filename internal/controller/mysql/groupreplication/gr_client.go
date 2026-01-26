package groupreplication

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type Config struct {
	Host          string
	Port          int
	GroupName     string
	Seeds         string
	Mode          string
	MemberWeight  int
	CheckInterval time.Duration
	Ordinal       int
	RootPassword  string
	ReplPassword  string
}

type Client struct {
	db *sql.DB
}

type GTIDSets struct {
	Executed  string
	Certified string
}

type GTIDInfo struct {
	Pod     string
	Ordinal int
	Sets    GTIDSets
}

// Waits for MySQL to be up and returns a client.
func New(ctx context.Context, dsn string) (*Client, error) {
	db, err := waitForMySQL(ctx, dsn, 2*time.Second)
	if err != nil {
		return nil, err
	}
	return &Client{db: db}, nil
}

// Close the client
func (c *Client) Close() error { return c.db.Close() }

// Creates Replication user if not present
func (c *Client) EnsureReplicationUser(ctx context.Context, pw string) error {
	return ensureReplicationUser(ctx, c.db, pw)
}

// Checks if any other pod is present in the group.
func (c *Client) AnyOnlineMember(ctx context.Context) (bool, error) {
	return anyOnlineMember(ctx, c.db)
}

// Check if pod is present in the group.
func (c *Client) SelfOnline(ctx context.Context) (bool, error) {
	return inGroup(ctx, c.db)
}

// Make the pod Bootstrap the group.
func (c *Client) Bootstrap(ctx context.Context, pw string) error {
	return bootstrapGroup(ctx, c.db, pw)
}

// Make the pod join the group.
func (c *Client) Join(ctx context.Context, pw string) error {
	return joinGroup(ctx, c.db, pw)
}

func (c *Client) GTIDSets(ctx context.Context) (GTIDSets, error) {
	var gtidExecuted, received string
	if err := c.db.QueryRowContext(ctx, "SELECT @@GLOBAL.GTID_EXECUTED").Scan(&gtidExecuted); err != nil {
		return GTIDSets{}, fmt.Errorf("gtid_executed: %w", err)
	}
	if err := c.db.QueryRowContext(ctx, `
        SELECT COALESCE(received_transaction_set, '')
        FROM performance_schema.replication_connection_status
        WHERE channel_name = 'group_replication_applier'
        LIMIT 1`,
	).Scan(&received); err != nil {
		if err != sql.ErrNoRows {
			return GTIDSets{}, fmt.Errorf("received transaction set: %w", err)
		}
		received = ""
	}

	return GTIDSets{Executed: gtidExecuted, Certified: received}, nil
}

// Checks for any possible online members to be present.
//
// Returns True if member is present.
// Returns false if not.
func anyOnlineMember(ctx context.Context, db *sql.DB) (bool, error) {
	var online string
	err := db.QueryRowContext(ctx, `
SELECT MEMBER_STATE FROM performance_schema.replication_group_members
WHERE MEMBER_STATE = 'ONLINE' LIMIT 1`).Scan(&online)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Waits for MySQL to be ready, utalizes the selectable functon
//
// Returns a DB Client
func waitForMySQL(ctx context.Context, dsn string, tick time.Duration) (*sql.DB, error) {
	for {
		db, err := sql.Open("mysql", dsn)
		if err == nil {
			if err = db.PingContext(ctx); err == nil {
				if err = ensureSelectable(ctx, db); err == nil {
					return db, nil
				}
			}
			_ = db.Close()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(tick):
		}
	}
}

// Ensures that GR queries can be performed, SQL readiness
func ensureSelectable(ctx context.Context, db *sql.DB) error {
	var one int
	return db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Ensures a replication user https://dev.mysql.com/doc/refman/8.4/en/group-replication-user-credentials.html //TODO: IMPLEMENT KEY SHARING
//
// Returns possible errors
func ensureReplicationUser(ctx context.Context, db *sql.DB, pw string) error {
	statements := []string{
		"SET SQL_LOG_BIN=0",
		"CREATE USER IF NOT EXISTS rpl_user@'%' IDENTIFIED WITH 'caching_sha2_password' BY ?",
		"GRANT REPLICATION SLAVE, CONNECTION_ADMIN, BACKUP_ADMIN, GROUP_REPLICATION_STREAM ON *.* TO rpl_user@'%'",
		"FLUSH PRIVILEGES",
		"SET SQL_LOG_BIN=1",
	}
	for _, q := range statements {
		if strings.Contains(q, "?") {
			if _, err := db.ExecContext(ctx, q, pw); err != nil {
				return fmt.Errorf("ensure replica user: %w", err)
			}
		} else {
			if _, err := db.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("ensure replica user: %w", err)
			}
		}
	}
	return nil
}

// Bootstrap statements https://dev.mysql.com/doc/refman/8.4/en/group-replication-bootstrap.html
//
// Returns possible errors
func bootstrapGroup(ctx context.Context, db *sql.DB, pw string) error {
	log.Println("bootstrapping group replication")
	statements := []string{
		"SET GLOBAL group_replication_bootstrap_group = ON",
		"START GROUP_REPLICATION USER='rpl_user', PASSWORD=?",
		"SET GLOBAL group_replication_bootstrap_group = OFF",
	}
	for _, q := range statements {
		if strings.Contains(q, "?") {
			if _, err := db.ExecContext(ctx, q, pw); err != nil {
				return fmt.Errorf("bootstrap: %w", err)
			}
		} else {
			if _, err := db.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("bootstrap: %w", err)
			}
		}
	}
	return nil
}

// Performs join query returns possible error
func joinGroup(ctx context.Context, db *sql.DB, pw string) error {
	if _, err := db.ExecContext(ctx, "START GROUP_REPLICATION USER='rpl_user', PASSWORD=?", pw); err != nil {
		return fmt.Errorf("join: %w", err)
	}
	log.Println("joined group replication")
	return nil
}

// Queries MEMBER_STATE returns true/false
func inGroup(ctx context.Context, db *sql.DB) (bool, error) {
	var state string
	err := db.QueryRowContext(ctx, `
SELECT MEMBER_STATE
FROM performance_schema.replication_group_members
WHERE MEMBER_ID = @@server_uuid;
`).Scan(&state)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.EqualFold(state, "ONLINE"), nil
}

func superset(ctx context.Context, db *sql.DB, candidate, other GTIDSets) (bool, error) {
	var diffExec, diffCert string
	if err := db.QueryRowContext(ctx, "SELECT GTID_SUBTRACT(?, ?)", other.Executed, candidate.Executed).Scan(&diffExec); err != nil {
		return false, fmt.Errorf("compare executed: %w", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT GTID_SUBTRACT(?, ?)", other.Certified, candidate.Certified).Scan(&diffCert); err != nil {
		return false, fmt.Errorf("compare certified: %w", err)
	}
	return diffExec == "" && diffCert == "", nil
}

func PickRestartCandidate(ctx context.Context, cmpDB *sql.DB, infos []GTIDInfo) (string, error) {
	if len(infos) == 0 {
		return "", fmt.Errorf("no GTID info to choose from")
	}

	for _, a := range infos {
		isSuperset := true
		for _, b := range infos {
			if a.Pod == b.Pod {
				continue
			}
			ok, err := superset(ctx, cmpDB, a.Sets, b.Sets)
			if err != nil {
				return "", err
			}
			if !ok {
				isSuperset = false
				break
			}
		}
		if isSuperset {
			return a.Pod, nil
		}
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Ordinal < infos[j].Ordinal
	})
	return infos[0].Pod, nil
}
