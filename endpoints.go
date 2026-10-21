// Dashboard API paths, observed from the Forethought web app on 2026-10-20/21.
// {id} = intent_definition_id; {wf} = workflow id. All are under dashboardAPI.
package main

import "fmt"

const (
	epIntents       = "/dashboard-controls/solve/v2/intents?include_inquiry_counts=false&product=workflow_builder"
	epIntentsCounts = "/dashboard-controls/solve/v2/intents?include_inquiry_counts=true&product=workflow_builder&start_timestamp=%d&end_timestamp=%d"
	epIntent        = "/dashboard-controls/solve/v2/workflow-builder/intent/%s"
	epIntentFlows   = "/dashboard-controls/solve/v2/workflow-builder/intent/%s/workflows"
	epEmailConfig   = "/solve/email/email-builder/intent_definition_id/%s/email-configurations"
	epVersionHist   = "/dashboard-controls/solve/autoflow/workflow-builder/%s/v2/version-history"
	epActions       = "/dashboard-controls/solve/v2/workflow-builder/actions"
	epContextVars   = "/dashboard-controls/solve/v2/workflow-builder/context-variables"
	epArticleSrc    = "/solve/email/email-builder/available-article-sources"
	epCurrentUser   = "/ssel/users/current-user"
)

func intentPath(id string) string      { return fmt.Sprintf(epIntent, id) }
func emailCfgPath(id string) string    { return fmt.Sprintf(epEmailConfig, id) }
func flowsPath(id string) string       { return fmt.Sprintf(epIntentFlows, id) }
func versionPath(wf string) string     { return fmt.Sprintf(epVersionHist, wf) }
func countsPath(from, to int64) string { return fmt.Sprintf(epIntentsCounts, from, to) }
