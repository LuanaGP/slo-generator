package slo

import (
	"fmt"
	"log"
	"strings"

	methods "github.com/globocom/slo-generator/methods"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/rulefmt"
)

var quantiles = []struct {
	name     string
	quantile float64
}{
	{
		name:     "p50",
		quantile: 0.5,
	},
	{
		name:     "p95",
		quantile: 0.95,
	},
	{
		name:     "p99",
		quantile: 0.99,
	},
}

type SLOSpec struct {
	SLOS []SLO
}

type ExprBlock struct {
	AlertMethod string `yaml:"alertMethod"`
	Expr        string `yaml:"expr"`
}

func (block *ExprBlock) ComputeExpr(window, le string) string {
	replacer := strings.NewReplacer("$window", window, "$le", le)
	return replacer.Replace(block.Expr)
}

func (block *ExprBlock) ComputeQuantile(window string, quantile float64) string {
	replacer := strings.NewReplacer("$window", window, "$quantile", fmt.Sprintf("%g", quantile))
	return replacer.Replace(block.Expr)
}

type SLO struct {
	Name       string `yaml:"name"`
	Objectives Objectives

	HonorLabels bool `yaml:"honorLabels"`

	TrafficRateRecord     ExprBlock         `yaml:"trafficRateRecord"`
	ErrorRateRecord       ExprBlock         `yaml:"errorRateRecord"`
	LatencyRecord         ExprBlock         `yaml:"latencyRecord"`
	LatencyQuantileRecord ExprBlock         `yaml:"latencyQuantileRecord"`
	Labels                map[string]string `yaml:"labels"`
	Annotations           map[string]string `yaml:"annotations"`
}

type Objectives struct {
	Availability float64                 `yaml:"availability"`
	Latency      []methods.LatencyTarget `yaml:"latency"`
}

func (slo *SLO) GenerateAlertRules() []rulefmt.Rule {
	alertRules := []rulefmt.Rule{}

	errorMethod := methods.Get(slo.ErrorRateRecord.AlertMethod)
	if errorMethod != nil {
		errorRules := errorMethod.AlertForError(slo.Name, slo.Objectives.Availability)
		alertRules = append(alertRules, errorRules...)
	}

	latencyMethod := methods.Get(slo.LatencyRecord.AlertMethod)
	if latencyMethod != nil {
		latencyRules := latencyMethod.AlertForLatency(slo.Name, slo.Objectives.Latency)
		alertRules = append(alertRules, latencyRules...)
	}

	for _, rule := range alertRules {
		slo.fillMetadata(&rule)
	}

	return alertRules
}

func (slo *SLO) fillMetadata(rule *rulefmt.Rule) {
	for label, value := range slo.Labels {
		rule.Labels[label] = value
	}

	for label, value := range slo.Annotations {
		rule.Annotations[label] = value
	}
}

func (slo *SLO) GenerateGroupRules() []rulefmt.RuleGroup {
	rules := []rulefmt.RuleGroup{}

	for _, sample := range defaultSamples {
		interval, err := model.ParseDuration(sample.Interval)
		if err != nil {
			log.Fatal(err)
		}
		ruleGroup := rulefmt.RuleGroup{
			Name:     "slo:" + slo.Name + ":" + sample.Name,
			Interval: interval,
			Rules:    []rulefmt.Rule{},
		}

		for _, bucket := range sample.Buckets {
			ruleGroup.Rules = append(ruleGroup.Rules, slo.generateRules(bucket)...)
		}

		if len(ruleGroup.Rules) > 0 {
			rules = append(rules, ruleGroup)
		}
	}

	return rules
}

func (slo *SLO) labels() map[string]string {
	labels := map[string]string{}
	if !slo.HonorLabels {
		labels["service"] = slo.Name
	}
	for key, value := range slo.Labels {
		labels[key] = value
	}
	return labels
}

func (slo *SLO) generateRules(bucket string) []rulefmt.Rule {
	rules := []rulefmt.Rule{}
	if slo.TrafficRateRecord.Expr != "" {
		trafficRateRecord := rulefmt.Rule{
			Record: "slo:service_traffic:ratio_rate_" + bucket,
			Expr:   slo.TrafficRateRecord.ComputeExpr(bucket, ""),
			Labels: slo.labels(),
		}

		rules = append(rules, trafficRateRecord)
	}

	if slo.ErrorRateRecord.Expr != "" {
		errorRateRecord := rulefmt.Rule{
			Record: "slo:service_errors_total:ratio_rate_" + bucket,
			Expr:   slo.ErrorRateRecord.ComputeExpr(bucket, ""),
			Labels: slo.labels(),
		}

		rules = append(rules, errorRateRecord)
	}

	if slo.LatencyQuantileRecord.Expr != "" {
		for _, quantile := range quantiles {
			latencyQuantileRecord := rulefmt.Rule{
				Record: "slo:service_latency:" + quantile.name + "_" + bucket,
				Expr:   slo.LatencyQuantileRecord.ComputeQuantile(bucket, quantile.quantile),
				Labels: slo.labels(),
			}

			rules = append(rules, latencyQuantileRecord)
		}
	}

	if slo.LatencyRecord.Expr != "" {
		for _, latencyBucket := range slo.Objectives.Latency {
			latencyRateRecord := rulefmt.Rule{
				Record: "slo:service_latency:ratio_rate_" + bucket,
				Expr:   slo.LatencyRecord.ComputeExpr(bucket, latencyBucket.LE),
				Labels: slo.labels(),
			}

			latencyRateRecord.Labels["le"] = latencyBucket.LE

			rules = append(rules, latencyRateRecord)
		}
	}

	return rules
}
