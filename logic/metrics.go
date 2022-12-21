package logic

import (
	"encoding/json"

	"github.com/gravitl/netmaker/database"
	"github.com/gravitl/netmaker/models"
)

// GetMetrics - gets the metrics
func GetMetrics(nodeid string) (*models.Metrics, error) {
	var metrics models.Metrics
	record, err := database.FetchRecord(database.METRICS_TABLE_NAME, nodeid)
	if err != nil {
		if database.IsEmptyRecord(err) {
			return &metrics, nil
		}
		return &metrics, err
	}
	err = json.Unmarshal([]byte(record), &metrics)
	if err != nil {
		return &metrics, err
	}
	return &metrics, nil
}

// UpdateMetrics - updates the metrics of a given client
func UpdateMetrics(nodeid string, metrics *models.Metrics) error {
	data, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	return database.Insert(nodeid, string(data), database.METRICS_TABLE_NAME)
}

// DeleteMetrics - deletes metrics of a given node
func DeleteMetrics(nodeid string) error {
	return database.DeleteRecord(database.METRICS_TABLE_NAME, nodeid)
}
