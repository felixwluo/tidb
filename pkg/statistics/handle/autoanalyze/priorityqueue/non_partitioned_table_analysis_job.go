// Copyright 2024 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package priorityqueue

import (
	"fmt"
	"strings"
	"time"

	"github.com/pingcap/tidb/pkg/sessionctx"
	"github.com/pingcap/tidb/pkg/sessionctx/sysproctrack"
	"github.com/pingcap/tidb/pkg/statistics/handle/autoanalyze/exec"
	statstypes "github.com/pingcap/tidb/pkg/statistics/handle/types"
	statsutil "github.com/pingcap/tidb/pkg/statistics/handle/util"
)

var _ AnalysisJob = &NonPartitionedTableAnalysisJob{}

const (
	analyzeTable analyzeType = "analyzeTable"
	analyzeIndex analyzeType = "analyzeIndex"
)

// NonPartitionedTableAnalysisJob is a TableAnalysisJob for analyzing the physical table.
type NonPartitionedTableAnalysisJob struct {
	successHook JobHook
	failureHook JobHook
	TableSchema string
	TableName   string
	// This is only for newly added indexes.
	Indexes []string
	Indicators
	TableID       int64
	TableStatsVer int
	Weight        float64
}

// NewNonPartitionedTableAnalysisJob creates a new TableAnalysisJob for analyzing the physical table.
func NewNonPartitionedTableAnalysisJob(
	schema, tableName string,
	tableID int64,
	indexes []string,
	tableStatsVer int,
	changePercentage float64,
	tableSize float64,
	lastAnalysisDuration time.Duration,
) *NonPartitionedTableAnalysisJob {
	return &NonPartitionedTableAnalysisJob{
		TableSchema:   schema,
		TableName:     tableName,
		TableID:       tableID,
		Indexes:       indexes,
		TableStatsVer: tableStatsVer,
		Indicators: Indicators{
			ChangePercentage:     changePercentage,
			TableSize:            tableSize,
			LastAnalysisDuration: lastAnalysisDuration,
		},
	}
}

// GetTableID gets the table ID of the job.
func (j *NonPartitionedTableAnalysisJob) GetTableID() int64 {
	return j.TableID
}

// Analyze analyzes the table or indexes.
func (j *NonPartitionedTableAnalysisJob) Analyze(
	statsHandle statstypes.StatsHandle,
	sysProcTracker sysproctrack.Tracker,
) error {
	success := true
	defer func() {
		if success {
			if j.successHook != nil {
				j.successHook(j)
			}
		} else {
			if j.failureHook != nil {
				j.failureHook(j)
			}
		}
	}()

	return statsutil.CallWithSCtx(statsHandle.SPool(), func(sctx sessionctx.Context) error {
		switch j.getAnalyzeType() {
		case analyzeTable:
			success = j.analyzeTable(sctx, statsHandle, sysProcTracker)
		case analyzeIndex:
			success = j.analyzeIndexes(sctx, statsHandle, sysProcTracker)
		}
		return nil
	})
}

// RegisterSuccessHook registers a successHook function that will be called after the job can be marked as successful.
func (j *NonPartitionedTableAnalysisJob) RegisterSuccessHook(hook JobHook) {
	j.successHook = hook
}

// RegisterFailureHook registers a failureHook function that will be called after the job can be marked as failed.
func (j *NonPartitionedTableAnalysisJob) RegisterFailureHook(hook JobHook) {
	j.failureHook = hook
}

// HasNewlyAddedIndex checks whether the table has newly added indexes.
func (j *NonPartitionedTableAnalysisJob) HasNewlyAddedIndex() bool {
	return len(j.Indexes) > 0
}

// IsValidToAnalyze checks whether the table is valid to analyze.
// We will check the last failed job and average analyze duration to determine whether the table is valid to analyze.
func (j *NonPartitionedTableAnalysisJob) IsValidToAnalyze(
	sctx sessionctx.Context,
) (bool, string) {
	if valid, failReason := isValidToAnalyze(
		sctx,
		j.TableSchema,
		j.TableName,
	); !valid {
		if j.failureHook != nil {
			j.failureHook(j)
		}

		return false, failReason
	}

	return true, ""
}

// SetWeight sets the weight of the job.
func (j *NonPartitionedTableAnalysisJob) SetWeight(weight float64) {
	j.Weight = weight
}

// GetWeight gets the weight of the job.
func (j *NonPartitionedTableAnalysisJob) GetWeight() float64 {
	return j.Weight
}

// GetIndicators returns the indicators of the table.
func (j *NonPartitionedTableAnalysisJob) GetIndicators() Indicators {
	return j.Indicators
}

// SetIndicators sets the indicators of the table.
func (j *NonPartitionedTableAnalysisJob) SetIndicators(indicators Indicators) {
	j.Indicators = indicators
}

// String implements fmt.Stringer interface.
func (j *NonPartitionedTableAnalysisJob) String() string {
	return fmt.Sprintf(
		"NonPartitionedTableAnalysisJob:\n"+
			"\tAnalyzeType: %s\n"+
			"\tIndexes: %s\n"+
			"\tSchema: %s\n"+
			"\tTable: %s\n"+
			"\tTableID: %d\n"+
			"\tTableStatsVer: %d\n"+
			"\tChangePercentage: %.6f\n"+
			"\tTableSize: %.2f\n"+
			"\tLastAnalysisDuration: %v\n"+
			"\tWeight: %.6f\n",
		j.getAnalyzeType(),
		strings.Join(j.Indexes, ", "),
		j.TableSchema, j.TableName, j.TableID, j.TableStatsVer,
		j.ChangePercentage, j.TableSize, j.LastAnalysisDuration, j.Weight,
	)
}
func (j *NonPartitionedTableAnalysisJob) getAnalyzeType() analyzeType {
	if j.HasNewlyAddedIndex() {
		return analyzeIndex
	}
	return analyzeTable
}

func (j *NonPartitionedTableAnalysisJob) analyzeTable(
	sctx sessionctx.Context,
	statsHandle statstypes.StatsHandle,
	sysProcTracker sysproctrack.Tracker,
) bool {
	sql, params := j.GenSQLForAnalyzeTable()
	return exec.AutoAnalyze(sctx, statsHandle, sysProcTracker, j.TableStatsVer, sql, params...)
}

// GenSQLForAnalyzeTable generates the SQL for analyzing the specified table.
func (j *NonPartitionedTableAnalysisJob) GenSQLForAnalyzeTable() (string, []any) {
	sql := "analyze table %n.%n"
	params := []any{j.TableSchema, j.TableName}

	return sql, params
}

func (j *NonPartitionedTableAnalysisJob) analyzeIndexes(
	sctx sessionctx.Context,
	statsHandle statstypes.StatsHandle,
	sysProcTracker sysproctrack.Tracker,
) bool {
	if len(j.Indexes) == 0 {
		return true
	}
	// For version 2, analyze one index will analyze all other indexes and columns.
	// For version 1, analyze one index will only analyze the specified index.
	analyzeVersion := sctx.GetSessionVars().AnalyzeVersion
	if analyzeVersion == 1 {
		for _, index := range j.Indexes {
			sql, params := j.GenSQLForAnalyzeIndex(index)
			if !exec.AutoAnalyze(sctx, statsHandle, sysProcTracker, j.TableStatsVer, sql, params...) {
				return false
			}
		}
		return true
	}
	// Only analyze the first index.
	// This is because analyzing a single index also analyzes all other indexes and columns.
	// Therefore, to avoid redundancy, we prevent multiple analyses of the same table.
	firstIndex := j.Indexes[0]
	sql, params := j.GenSQLForAnalyzeIndex(firstIndex)
	return exec.AutoAnalyze(sctx, statsHandle, sysProcTracker, j.TableStatsVer, sql, params...)
}

// GenSQLForAnalyzeIndex generates the SQL for analyzing the specified index.
func (j *NonPartitionedTableAnalysisJob) GenSQLForAnalyzeIndex(index string) (string, []any) {
	sql := "analyze table %n.%n index %n"
	params := []any{j.TableSchema, j.TableName, index}

	return sql, params
}
