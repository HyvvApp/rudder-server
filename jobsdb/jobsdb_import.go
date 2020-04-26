package jobsdb

import (
	"database/sql"
	"fmt"
	"math"

	"github.com/rudderlabs/rudder-server/config"
	"github.com/rudderlabs/rudder-server/utils/logger"
)

func (jd *HandleT) dsForImportEventsSetup(dsList []dataSetT, ds dataSetT) (dataSetT, bool) {
	var shouldCheckpoint bool
	if ds.Index == "" {
		ds = jd.addNewDS(false, jd.migrationState.DsForNewEvents)
		logger.Infof("Should Checkpoint Import Setup event for the new ds : %v", ds)
		shouldCheckpoint = true
	}

	if jd.migrationState.sequenceProvider == nil || !jd.migrationState.sequenceProvider.IsInitialized() {
		dsList = jd.getDSList(true)
		importDSMin := jd.getMaxIDForDs(ds)

		if importDSMin == 0 {
			for idx, dataSet := range dsList {
				if dataSet.Index == ds.Index {
					if idx > 0 {
						importDSMin = jd.getMaxIDForDs(dsList[idx-1])
					}
				}
			}
		}
		jd.migrationState.sequenceProvider = NewSequenceProvider(importDSMin + 1)
	}
	return ds, shouldCheckpoint
}

func (jd *HandleT) dsForNewEventsSetup(dsList []dataSetT, ds dataSetT) (dataSetT, bool) {
	dsListLen := len(dsList)

	if ds.Index == "" {
		if jd.isEmpty(dsList[dsListLen-1]) {
			ds = dsList[dsListLen-1]
		} else {
			dsForNewEvents := jd.addNewDS(true, dataSetT{})
			ds = dsForNewEvents
		}

		seqNoForNewDS := int64(getNewVersion()) * int64(math.Pow10(13))
		jd.updateSequenceNumber(ds, seqNoForNewDS)
		logger.Infof("Jobsdb: New dataSet %s is prepared with start sequence : %d", ds, seqNoForNewDS)
		return ds, true
	}
	return ds, false
}

func getNewVersion() int {
	//TODO: revisit this
	return config.GetRequiredEnvAsInt("CLUSTER_VERSION")
}

//StoreImportedJobsAndJobStatuses is used to write the jobs to _tables
func (jd *HandleT) StoreImportedJobsAndJobStatuses(jobList []*JobT, fileName string, migrationEvent MigrationEvent) {
	if jd.migrationState.DsForImport.Index == "" {
		jd.migrationState.DsForImport = jd.setupFor(ImportOp, jd.migrationState.DsForImport, jd.dsForImportEventsSetup)
	}

	if jd.migrationState.DsForImport.Index == "" {
		panic("dsForImportEvents was not setup. Go debug")
	}

	startJobID := jd.getStartJobID(len(jobList), migrationEvent)
	statusList := []*JobStatusT{}

	for idx, job := range jobList {
		jobID := startJobID + int64(idx)
		job.JobID = jobID
		job.LastJobStatus.JobID = jobID
		if job.LastJobStatus.JobState != "" {
			statusList = append(statusList, &job.LastJobStatus)
		}
	}

	//TODO: modify storeJobsDS and updateJobStatusDS to accept an additional bool to support "on conflict do nothing"
	//what is retry each expected to do?
	jd.storeJobsDS(jd.migrationState.DsForImport, true, true, jobList)
	jd.updateJobStatusDS(jd.migrationState.DsForImport, statusList, []string{}, []ParameterFilterT{})
}

func (jd *HandleT) getStartJobID(count int, migrationEvent MigrationEvent) int64 {
	var sequenceNumber int64
	sequenceNumber = 0
	sequenceNumber = jd.getSeqNoForFileFromDB(migrationEvent.FileLocation, ImportOp)
	if sequenceNumber == 0 {
		sequenceNumber = jd.migrationState.sequenceProvider.ReserveIds(count)
	}
	migrationEvent.StartSeq = sequenceNumber
	jd.Checkpoint(&migrationEvent)
	return sequenceNumber
}

func (jd *HandleT) updateSequenceNumber(ds dataSetT, sequenceNumber int64) {
	sqlStatement := fmt.Sprintf(`SELECT setval('%s_jobs_%s_job_id_seq', %d)`,
		jd.tablePrefix, ds.Index, sequenceNumber)
	_, err := jd.dbHandle.Exec(sqlStatement)
	if err != nil {
		panic(err.Error())
	}
}

func (jd *HandleT) getMaxIDForDs(ds dataSetT) int64 {
	var maxID sql.NullInt64
	sqlStatement := fmt.Sprintf(`SELECT MAX(job_id) FROM %s`, ds.JobTable)
	row := jd.dbHandle.QueryRow(sqlStatement)
	err := row.Scan(&maxID)
	jd.assertError(err)

	if maxID.Valid {
		return int64(maxID.Int64)
	}
	return int64(0)

}