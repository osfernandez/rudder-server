package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/rudderlabs/rudder-server/jobsdb"
)

type GatewayAdmin struct {
	handle *HandleT
}

var prefix = "gw_jobs_"

// Status function is used for debug purposes by the admin interface
func (g *GatewayAdmin) Status() interface{} {
	configSubscriberLock.RLock()
	defer configSubscriberLock.RUnlock()
	writeKeys := make([]string, 0, len(enabledWriteKeysSourceMap))
	for k := range enabledWriteKeysSourceMap {
		writeKeys = append(writeKeys, k)
	}

	return map[string]interface{}{
		"ack-count":          g.handle.ackCount,
		"recv-count":         g.handle.recvCount,
		"enabled-write-keys": writeKeys,
		"jobsdb":             g.handle.jobsDB.Status(),
	}

}

type GatewayRPCHandler struct {
	jobsDB jobsdb.JobsDB
}

type SqlRunner struct {
	dbHandle     *sql.DB
	jobTableName string
	err          error
}

type SourceEvents struct {
	Count  int
	Source string
}

func (r *SqlRunner) getUniqueSources() []SourceEvents {
	sources := make([]SourceEvents, 0)
	if r.err != nil {
		return sources
	}
	uniqueSourceValsStmt := fmt.Sprintf(`select count(*) as count, parameters -> 'source_id' as source from %s group by parameters -> 'source_id';`, r.jobTableName)
	var rows *sql.Rows
	rows, r.err = r.dbHandle.Query(uniqueSourceValsStmt)
	if r.err != nil {
		return sources
	}
	defer rows.Close()
	sourceEvent := SourceEvents{}
	for rows.Next() {
		r.err = rows.Scan(&sourceEvent.Count, &sourceEvent.Source)
		if r.err != nil {
			return sources // defer closing of rows, so return will not memory leak
		}
		sources = append(sources, sourceEvent)
	}

	r.err = rows.Err()
	if r.err != nil {
		return sources
	}

	r.err = rows.Close() // rows.close will be called in defer too, but it should be harmless to call multiple times
	return sources
}

func (r *SqlRunner) getNumUniqueUsers() int {
	var numUsers int
	if r.err != nil {
		return 0
	}
	numUniqueUsersStmt := fmt.Sprintf(`select count(*) from (select  distinct user_id from %s) as t`, r.jobTableName)
	r.err = runSQL(r, numUniqueUsersStmt, &numUsers)
	return numUsers
}

func (r *SqlRunner) getAvgBatchSize() float64 {
	var batchSize sql.NullFloat64
	var avgBatchSize float64
	if r.err != nil {
		return 0
	}
	avgBatchSizeStmt := fmt.Sprintf(`select avg(jsonb_array_length(batch)) from (select event_payload->'batch' as batch from %s) t`, r.jobTableName)
	r.err = runSQL(r, avgBatchSizeStmt, &batchSize)
	if batchSize.Valid {
		avgBatchSize = batchSize.Float64
	}
	return avgBatchSize
}

func (r *SqlRunner) getTableSize() int64 {
	var tableSize int64
	if r.err != nil {
		return 0
	}
	tableSizeStmt := fmt.Sprintf(`select pg_total_relation_size('%s')`, r.jobTableName)
	r.err = runSQL(r, tableSizeStmt, &tableSize)
	return tableSize
}

func (r *SqlRunner) getTableRowCount() int {
	var numRows int
	if r.err != nil {
		return 0
	}
	totalRowsStmt := fmt.Sprintf(`select count(*) from %s`, r.jobTableName)
	r.err = runSQL(r, totalRowsStmt, &numRows)
	return numRows
}

type DSStats struct {
	SourceNums   []SourceEvents
	NumUsers     int
	AvgBatchSize float64
	TableSize    int64
	NumRows      int
}

// first_event, last_event min--maxid to event: available in dsrange?
// Average batch size ⇒ num_events we want per ds
// writeKey, count(*)  we want source name to count per ds
// Num Distinct users per ds
// Avg Event size = Table_size / (avg Batch size * Total rows) is Table_size correct measure?
// add job status group by
func (g *GatewayRPCHandler) GetDSStats(dsName string, result *string) error {
	jobTableName := prefix + dsName
	dbHandle, err := sql.Open("postgres", jobsdb.GetConnectionString())
	defer dbHandle.Close() // since this also returns an error, we can explicitly close but not doing
	runner := &SqlRunner{dbHandle: dbHandle, jobTableName: jobTableName, err: err}
	sources := runner.getUniqueSources()
	numUsers := runner.getNumUniqueUsers()
	avgBatchSize := runner.getAvgBatchSize()
	tableSize := runner.getTableSize()
	numRows := runner.getTableRowCount()

	configSubscriberLock.RLock()
	sourcesEventToCounts := make([]SourceEvents, 0)
	for _, sourceEvent := range sources {
		name, found := sourceIDToNameMap[sourceEvent.Source[1:len(sourceEvent.Source)-1]]
		if found {
			sourcesEventToCounts = append(sourcesEventToCounts, SourceEvents{sourceEvent.Count, name})
		}
	}
	configSubscriberLock.RUnlock()
	response, err := json.MarshalIndent(DSStats{sourcesEventToCounts, numUsers, avgBatchSize, tableSize, numRows}, "", " ")
	if err != nil {
		*result = ""
		runner.err = err
	} else {
		*result = string(response)
	}

	return runner.err
}

func runSQL(runner *SqlRunner, query string, reciever interface{}) error {
	row := runner.dbHandle.QueryRow(query)
	err := row.Scan(reciever)
	if err != nil {
		if err == sql.ErrNoRows {
			fmt.Println("Zero rows found")
		}
	}
	return err
}
