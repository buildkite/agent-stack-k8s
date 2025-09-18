{{ if .Values.monitoring.deployGrafanaDashboard }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "agent-stack-k8s.fullname" . }}-grafana-dashboard
  namespace: {{ with .Values.monitoring.namespace }}{{ . }}{{ else }}{{ .Release.Namespace }}{{ end }}
  labels:
    grafana_dashboard: "1"
    app.kubernetes.io/name: grafana
data:
  agent-stack-k8s.json: |-
    {
      "__inputs": [],
      "__elements": {},
      "__requires": [
        {
          "type": "grafana",
          "id": "grafana",
          "name": "Grafana",
          "version": "12.2.0-16791878397"
        },
        {
          "type": "panel",
          "id": "heatmap",
          "name": "Heatmap",
          "version": ""
        },
        {
          "type": "datasource",
          "id": "prometheus",
          "name": "Prometheus",
          "version": "1.0.0"
        },
        {
          "type": "panel",
          "id": "timeseries",
          "name": "Time series",
          "version": ""
        }
      ],
      "annotations": {
        "list": [
          {
            "builtIn": 1,
            "datasource": {
              "type": "grafana",
              "uid": "-- Grafana --"
            },
            "enable": true,
            "hide": true,
            "iconColor": "rgba(0, 211, 255, 1)",
            "name": "Annotations & Alerts",
            "type": "dashboard"
          }
        ]
      },
      "description": "Displays Prometheus metrics scraped from Buildkite Agent Stack for Kubernetes. Some panels assume Prometheus has Native Histograms enabled.",
      "editable": true,
      "fiscalYearStartMonth": 0,
      "graphTooltip": 1,
      "id": null,
      "links": [],
      "panels": [
        {
          "collapsed": false,
          "gridPos": {
            "h": 1,
            "w": 24,
            "x": 0,
            "y": 0
          },
          "id": 21,
          "panels": [],
          "title": "Overview",
          "type": "row"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "This heatmap shows the duration between receiving a job from Buildkite and creating the corresponding job in Kubernetes. Note this chart only functions with native histograms enabled in Prometheus.",
          "fieldConfig": {
            "defaults": {
              "custom": {
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "scaleDistribution": {
                  "type": "linear"
                }
              }
            },
            "overrides": []
          },
          "gridPos": {
            "h": 9,
            "w": 24,
            "x": 0,
            "y": 1
          },
          "id": 6,
          "options": {
            "calculate": false,
            "cellGap": 1,
            "color": {
              "exponent": 0.5,
              "fill": "dark-orange",
              "mode": "scheme",
              "reverse": false,
              "scale": "exponential",
              "scheme": "Oranges",
              "steps": 64
            },
            "exemplars": {
              "color": "rgba(255,0,255,0.7)"
            },
            "filterValues": {
              "le": 1e-9
            },
            "legend": {
              "show": true
            },
            "rowsFrame": {
              "layout": "auto"
            },
            "tooltip": {
              "mode": "single",
              "showColorScale": false,
              "yHistogram": false
            },
            "yAxis": {
              "axisPlacement": "left",
              "reverse": false,
              "unit": "s"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_job_end_to_end_seconds{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "End-to-end processing time",
              "range": true,
              "refId": "A"
            }
          ],
          "title": "End-to-end processing time",
          "type": "heatmap"
        },
        {
          "collapsed": false,
          "gridPos": {
            "h": 1,
            "w": 24,
            "x": 0,
            "y": 10
          },
          "id": 7,
          "panels": [],
          "title": "Monitor",
          "type": "row"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "The monitor is the first component in the stack. It periodically queries Buildkite for new jobs to run.",
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "custom": {
                "axisBorderShow": false,
                "axisCenteredZero": false,
                "axisColorMode": "text",
                "axisLabel": "",
                "axisPlacement": "auto",
                "barAlignment": 0,
                "barWidthFactor": 0.6,
                "drawStyle": "line",
                "fillOpacity": 0,
                "gradientMode": "none",
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "insertNulls": false,
                "lineInterpolation": "linear",
                "lineWidth": 1,
                "pointSize": 5,
                "scaleDistribution": {
                  "type": "linear"
                },
                "showPoints": "auto",
                "spanNulls": false,
                "stacking": {
                  "group": "A",
                  "mode": "none"
                },
                "thresholdsStyle": {
                  "mode": "off"
                }
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {
                    "color": "green",
                    "value": 0
                  }
                ]
              },
              "unit": "jobs/s"
            },
            "overrides": []
          },
          "gridPos": {
            "h": 9,
            "w": 12,
            "x": 0,
            "y": 11
          },
          "id": 1,
          "options": {
            "legend": {
              "calcs": [],
              "displayMode": "list",
              "placement": "bottom",
              "showLegend": true
            },
            "tooltip": {
              "hideZeros": false,
              "mode": "multi",
              "sort": "none"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_monitor_jobs_returned_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "Returned from query",
              "range": true,
              "refId": "A"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_monitor_jobs_handled_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Passed to limiter",
              "range": true,
              "refId": "B"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_monitor_jobs_filtered_out_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Filtered out",
              "range": true,
              "refId": "C"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_monitor_job_handler_errors_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Next handler error",
              "range": true,
              "refId": "D"
            }
          ],
          "title": "Monitor job rate",
          "type": "timeseries"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "This heatmap shows the duration of time spent querying Buildkite for jobs. Note this chart only functions with native histograms enabled in Prometheus.",
          "fieldConfig": {
            "defaults": {
              "custom": {
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "scaleDistribution": {
                  "type": "linear"
                }
              }
            },
            "overrides": []
          },
          "gridPos": {
            "h": 9,
            "w": 12,
            "x": 12,
            "y": 11
          },
          "id": 5,
          "options": {
            "calculate": false,
            "cellGap": 1,
            "color": {
              "exponent": 0.5,
              "fill": "dark-orange",
              "mode": "scheme",
              "reverse": false,
              "scale": "exponential",
              "scheme": "Oranges",
              "steps": 64
            },
            "exemplars": {
              "color": "rgba(255,0,255,0.7)"
            },
            "filterValues": {
              "le": 1e-9
            },
            "legend": {
              "show": true
            },
            "rowsFrame": {
              "layout": "auto"
            },
            "tooltip": {
              "mode": "single",
              "showColorScale": false,
              "yHistogram": false
            },
            "yAxis": {
              "axisPlacement": "left",
              "reverse": false,
              "unit": "s"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_monitor_job_query_seconds{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "Returned from query",
              "range": true,
              "refId": "A"
            }
          ],
          "title": "Monitor query time",
          "type": "heatmap"
        },
        {
          "collapsed": false,
          "gridPos": {
            "h": 1,
            "w": 24,
            "x": 0,
            "y": 20
          },
          "id": 8,
          "panels": [],
          "title": "Limiter",
          "type": "row"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "The limiter is the second component in the stack. It has an internal queue of jobs, and also applies the max-in-flight limit (if configured).",
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "custom": {
                "axisBorderShow": false,
                "axisCenteredZero": false,
                "axisColorMode": "text",
                "axisLabel": "",
                "axisPlacement": "auto",
                "barAlignment": 0,
                "barWidthFactor": 0.6,
                "drawStyle": "line",
                "fillOpacity": 0,
                "gradientMode": "none",
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "insertNulls": false,
                "lineInterpolation": "linear",
                "lineWidth": 1,
                "pointSize": 5,
                "scaleDistribution": {
                  "type": "linear"
                },
                "showPoints": "auto",
                "spanNulls": false,
                "stacking": {
                  "group": "A",
                  "mode": "none"
                },
                "thresholdsStyle": {
                  "mode": "off"
                }
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {
                    "color": "green",
                    "value": 0
                  }
                ]
              }
            },
            "overrides": []
          },
          "gridPos": {
            "h": 9,
            "w": 12,
            "x": 0,
            "y": 21
          },
          "id": 2,
          "options": {
            "legend": {
              "calcs": [],
              "displayMode": "list",
              "placement": "bottom",
              "showLegend": true
            },
            "tooltip": {
              "hideZeros": false,
              "mode": "multi",
              "sort": "none"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(buildkite_limiter_work_queue_length{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
              "hide": false,
              "legendFormat": "Jobs in queue",
              "range": true,
              "refId": "A"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(buildkite_limiter_max_in_flight{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
              "hide": false,
              "instant": false,
              "legendFormat": "Max in flight",
              "range": true,
              "refId": "B"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(buildkite_limiter_tokens_available{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
              "hide": false,
              "instant": false,
              "legendFormat": "Tokens available",
              "range": true,
              "refId": "C"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(buildkite_limiter_waiting_for_token{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
              "hide": false,
              "instant": false,
              "legendFormat": "Workers waiting for token",
              "range": true,
              "refId": "D"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(buildkite_limiter_waiting_for_work{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
              "hide": false,
              "instant": false,
              "legendFormat": "Workers waiting for work",
              "range": true,
              "refId": "E"
            }
          ],
          "title": "Limiter job queue",
          "type": "timeseries"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "The limiter is the second component in the stack. It has an internal queue of jobs, and also applies the max-in-flight limit (if configured).",
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "custom": {
                "axisBorderShow": false,
                "axisCenteredZero": false,
                "axisColorMode": "text",
                "axisLabel": "",
                "axisPlacement": "auto",
                "barAlignment": 0,
                "barWidthFactor": 0.6,
                "drawStyle": "line",
                "fillOpacity": 0,
                "gradientMode": "none",
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "insertNulls": false,
                "lineInterpolation": "linear",
                "lineWidth": 1,
                "pointSize": 5,
                "scaleDistribution": {
                  "type": "linear"
                },
                "showPoints": "auto",
                "spanNulls": false,
                "stacking": {
                  "group": "A",
                  "mode": "none"
                },
                "thresholdsStyle": {
                  "mode": "off"
                }
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {
                    "color": "green",
                    "value": 0
                  }
                ]
              },
              "unit": "jobs/s"
            },
            "overrides": []
          },
          "gridPos": {
            "h": 9,
            "w": 12,
            "x": 12,
            "y": 21
          },
          "id": 11,
          "options": {
            "legend": {
              "calcs": [],
              "displayMode": "list",
              "placement": "bottom",
              "showLegend": true
            },
            "tooltip": {
              "hideZeros": false,
              "mode": "multi",
              "sort": "none"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_limiter_job_handler_calls_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "Passed to deduper",
              "range": true,
              "refId": "A"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum by(reason) (rate(buildkite_limiter_job_handler_errors_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Error from deduper - {{"{{"}}reason{{"}}"}}",
              "range": true,
              "refId": "B"
            }
          ],
          "title": "Limiter job rate",
          "type": "timeseries"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "This heatmap shows the duration of time each limiter worker spent waiting for new work. Note this chart only functions with native histograms enabled in Prometheus.",
          "fieldConfig": {
            "defaults": {
              "custom": {
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "scaleDistribution": {
                  "type": "linear"
                }
              }
            },
            "overrides": []
          },
          "gridPos": {
            "h": 9,
            "w": 12,
            "x": 0,
            "y": 30
          },
          "id": 12,
          "options": {
            "calculate": false,
            "cellGap": 1,
            "color": {
              "exponent": 0.5,
              "fill": "dark-orange",
              "mode": "scheme",
              "reverse": false,
              "scale": "exponential",
              "scheme": "Oranges",
              "steps": 64
            },
            "exemplars": {
              "color": "rgba(255,0,255,0.7)"
            },
            "filterValues": {
              "le": 1e-9
            },
            "legend": {
              "show": true
            },
            "rowsFrame": {
              "layout": "auto"
            },
            "tooltip": {
              "mode": "single",
              "showColorScale": false,
              "yHistogram": false
            },
            "yAxis": {
              "axisPlacement": "left",
              "reverse": false,
              "unit": "s"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_limiter_work_wait_duration_seconds{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "Time waiting for work",
              "range": true,
              "refId": "A"
            }
          ],
          "title": "Limiter worker duration waiting for work",
          "type": "heatmap"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "This heatmap shows the duration of time each limiter worker spent waiting for a limiter token. Note this chart only functions with native histograms enabled in Prometheus.",
          "fieldConfig": {
            "defaults": {
              "custom": {
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "scaleDistribution": {
                  "type": "linear"
                }
              }
            },
            "overrides": []
          },
          "gridPos": {
            "h": 9,
            "w": 12,
            "x": 12,
            "y": 30
          },
          "id": 13,
          "options": {
            "calculate": false,
            "cellGap": 1,
            "color": {
              "exponent": 0.5,
              "fill": "dark-orange",
              "mode": "scheme",
              "reverse": false,
              "scale": "exponential",
              "scheme": "Oranges",
              "steps": 64
            },
            "exemplars": {
              "color": "rgba(255,0,255,0.7)"
            },
            "filterValues": {
              "le": 1e-9
            },
            "legend": {
              "show": true
            },
            "rowsFrame": {
              "layout": "auto"
            },
            "tooltip": {
              "mode": "single",
              "showColorScale": false,
              "yHistogram": false
            },
            "yAxis": {
              "axisPlacement": "left",
              "reverse": false,
              "unit": "s"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_limiter_token_wait_duration_seconds{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "Time waiting for token",
              "range": true,
              "refId": "B"
            }
          ],
          "title": "Limiter worker duration waiting for token",
          "type": "heatmap"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "",
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "custom": {
                "axisBorderShow": false,
                "axisCenteredZero": false,
                "axisColorMode": "text",
                "axisLabel": "",
                "axisPlacement": "auto",
                "barAlignment": 0,
                "barWidthFactor": 0.6,
                "drawStyle": "line",
                "fillOpacity": 0,
                "gradientMode": "none",
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "insertNulls": false,
                "lineInterpolation": "linear",
                "lineWidth": 1,
                "pointSize": 5,
                "scaleDistribution": {
                  "type": "linear"
                },
                "showPoints": "auto",
                "spanNulls": false,
                "stacking": {
                  "group": "A",
                  "mode": "none"
                },
                "thresholdsStyle": {
                  "mode": "off"
                }
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {
                    "color": "green",
                    "value": 0
                  }
                ]
              },
              "unit": "eps"
            },
            "overrides": []
          },
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 0,
            "y": 39
          },
          "id": 26,
          "options": {
            "legend": {
              "calcs": [],
              "displayMode": "list",
              "placement": "bottom",
              "showLegend": true
            },
            "tooltip": {
              "hideZeros": false,
              "mode": "multi",
              "sort": "none"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_limiter_onadd_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "OnAdd events",
              "range": true,
              "refId": "E"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_limiter_ondelete_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "OnDelete events",
              "range": true,
              "refId": "A"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_limiter_onupdate_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "OnUpdate events",
              "range": true,
              "refId": "B"
            }
          ],
          "title": "Limiter informer event rate",
          "type": "timeseries"
        },
        {
          "collapsed": false,
          "gridPos": {
            "h": 1,
            "w": 24,
            "x": 0,
            "y": 47
          },
          "id": 9,
          "panels": [],
          "title": "Deduper",
          "type": "row"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "The deduper is the third component in the stack. It prevents duplicate jobs from being scheduled and has the most accurate estimate of jobs present on Kubernetes, which includes both running jobs and completed jobs not yet cleaned up.",
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "custom": {
                "axisBorderShow": false,
                "axisCenteredZero": false,
                "axisColorMode": "text",
                "axisLabel": "",
                "axisPlacement": "auto",
                "barAlignment": 0,
                "barWidthFactor": 0.6,
                "drawStyle": "line",
                "fillOpacity": 0,
                "gradientMode": "none",
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "insertNulls": false,
                "lineInterpolation": "linear",
                "lineWidth": 1,
                "pointSize": 5,
                "scaleDistribution": {
                  "type": "linear"
                },
                "showPoints": "auto",
                "spanNulls": false,
                "stacking": {
                  "group": "A",
                  "mode": "none"
                },
                "thresholdsStyle": {
                  "mode": "off"
                }
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {
                    "color": "green",
                    "value": 0
                  }
                ]
              },
              "unit": "jobs"
            },
            "overrides": []
          },
          "gridPos": {
            "h": 9,
            "w": 12,
            "x": 0,
            "y": 48
          },
          "id": 14,
          "options": {
            "legend": {
              "calcs": [],
              "displayMode": "list",
              "placement": "bottom",
              "showLegend": true
            },
            "tooltip": {
              "hideZeros": false,
              "mode": "multi",
              "sort": "none"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(buildkite_deduper_jobs_running{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
              "hide": false,
              "legendFormat": "Jobs tracked by deduper",
              "range": true,
              "refId": "A"
            }
          ],
          "title": "Deduper job count",
          "type": "timeseries"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "The deduper is the third component in the stack. It prevents duplicate jobs from being scheduled. ",
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "custom": {
                "axisBorderShow": false,
                "axisCenteredZero": false,
                "axisColorMode": "text",
                "axisLabel": "",
                "axisPlacement": "auto",
                "barAlignment": 0,
                "barWidthFactor": 0.6,
                "drawStyle": "line",
                "fillOpacity": 0,
                "gradientMode": "none",
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "insertNulls": false,
                "lineInterpolation": "linear",
                "lineWidth": 1,
                "pointSize": 5,
                "scaleDistribution": {
                  "type": "linear"
                },
                "showPoints": "auto",
                "spanNulls": false,
                "stacking": {
                  "group": "A",
                  "mode": "none"
                },
                "thresholdsStyle": {
                  "mode": "off"
                }
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {
                    "color": "green",
                    "value": 0
                  }
                ]
              },
              "unit": "jobs/s"
            },
            "overrides": []
          },
          "gridPos": {
            "h": 9,
            "w": 12,
            "x": 12,
            "y": 48
          },
          "id": 4,
          "options": {
            "legend": {
              "calcs": [],
              "displayMode": "list",
              "placement": "bottom",
              "showLegend": true
            },
            "tooltip": {
              "hideZeros": false,
              "mode": "multi",
              "sort": "none"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_deduper_job_handler_calls_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "Passed to scheduler",
              "range": true,
              "refId": "A"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_deduper_job_handler_errors_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Error from scheduler",
              "range": true,
              "refId": "B"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_deduper_jobs_unmarked_running_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Un-marked as running",
              "range": true,
              "refId": "C"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_deduper_jobs_marked_running_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Marked as running",
              "range": true,
              "refId": "D"
            }
          ],
          "title": "Deduper job rate",
          "type": "timeseries"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "",
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "custom": {
                "axisBorderShow": false,
                "axisCenteredZero": false,
                "axisColorMode": "text",
                "axisLabel": "",
                "axisPlacement": "auto",
                "barAlignment": 0,
                "barWidthFactor": 0.6,
                "drawStyle": "line",
                "fillOpacity": 0,
                "gradientMode": "none",
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "insertNulls": false,
                "lineInterpolation": "linear",
                "lineWidth": 1,
                "pointSize": 5,
                "scaleDistribution": {
                  "type": "linear"
                },
                "showPoints": "auto",
                "spanNulls": false,
                "stacking": {
                  "group": "A",
                  "mode": "none"
                },
                "thresholdsStyle": {
                  "mode": "off"
                }
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {
                    "color": "green",
                    "value": 0
                  }
                ]
              },
              "unit": "eps"
            },
            "overrides": []
          },
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 0,
            "y": 57
          },
          "id": 20,
          "options": {
            "legend": {
              "calcs": [],
              "displayMode": "list",
              "placement": "bottom",
              "showLegend": true
            },
            "tooltip": {
              "hideZeros": false,
              "mode": "multi",
              "sort": "none"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_deduper_onadd_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "OnAdd events",
              "range": true,
              "refId": "E"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_deduper_ondelete_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "OnDelete events",
              "range": true,
              "refId": "A"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_deduper_onupdate_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "OnUpdate events",
              "range": true,
              "refId": "B"
            }
          ],
          "title": "Deduper informer event rate",
          "type": "timeseries"
        },
        {
          "collapsed": false,
          "gridPos": {
            "h": 1,
            "w": 24,
            "x": 0,
            "y": 65
          },
          "id": 10,
          "panels": [],
          "title": "Scheduler",
          "type": "row"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "description": "The scheduler is the last component in the stack prior to jobs running. It is responsible for passing the job onto Kubernetes.",
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "custom": {
                "axisBorderShow": false,
                "axisCenteredZero": false,
                "axisColorMode": "text",
                "axisLabel": "",
                "axisPlacement": "auto",
                "barAlignment": 0,
                "barWidthFactor": 0.6,
                "drawStyle": "line",
                "fillOpacity": 0,
                "gradientMode": "none",
                "hideFrom": {
                  "legend": false,
                  "tooltip": false,
                  "viz": false
                },
                "insertNulls": false,
                "lineInterpolation": "linear",
                "lineWidth": 1,
                "pointSize": 5,
                "scaleDistribution": {
                  "type": "linear"
                },
                "showPoints": "auto",
                "spanNulls": false,
                "stacking": {
                  "group": "A",
                  "mode": "none"
                },
                "thresholdsStyle": {
                  "mode": "off"
                }
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {
                    "color": "green",
                    "value": 0
                  }
                ]
              },
              "unit": "jobs/s"
            },
            "overrides": []
          },
          "gridPos": {
            "h": 9,
            "w": 12,
            "x": 0,
            "y": 66
          },
          "id": 3,
          "options": {
            "legend": {
              "calcs": [],
              "displayMode": "list",
              "placement": "bottom",
              "showLegend": true
            },
            "tooltip": {
              "hideZeros": false,
              "mode": "multi",
              "sort": "none"
            }
          },
          "pluginVersion": "12.2.0-16791878397",
          "targets": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_scheduler_job_create_calls_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "legendFormat": "Create attempted",
              "range": true,
              "refId": "A"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_scheduler_job_create_success_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Create succeeded",
              "range": true,
              "refId": "B"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum by (reason) (rate(buildkite_scheduler_job_create_errors_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Create error - {{"{{"}}reason{{"}}"}}",
              "range": true,
              "refId": "C"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_scheduler_jobs_failed_on_buildkite_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Creation failed, reported",
              "range": true,
              "refId": "D"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "editorMode": "builder",
              "expr": "sum(rate(buildkite_pod_watcher_job_fail_on_buildkite_errors_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
              "hide": false,
              "instant": false,
              "legendFormat": "Creation failed, error while reporting",
              "range": true,
              "refId": "E"
            }
          ],
          "title": "Scheduler job rate",
          "type": "timeseries"
        },
        {
          "collapsed": true,
          "gridPos": {
            "h": 1,
            "w": 24,
            "x": 0,
            "y": 75
          },
          "id": 16,
          "panels": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "description": "The job watcher is an auxiliary component that watches for certain Kubernetes job conditions, such as stalling or finishing without ever starting a pod.",
              "fieldConfig": {
                "defaults": {
                  "color": {
                    "mode": "palette-classic"
                  },
                  "custom": {
                    "axisBorderShow": false,
                    "axisCenteredZero": false,
                    "axisColorMode": "text",
                    "axisLabel": "",
                    "axisPlacement": "auto",
                    "barAlignment": 0,
                    "barWidthFactor": 0.6,
                    "drawStyle": "line",
                    "fillOpacity": 0,
                    "gradientMode": "none",
                    "hideFrom": {
                      "legend": false,
                      "tooltip": false,
                      "viz": false
                    },
                    "insertNulls": false,
                    "lineInterpolation": "linear",
                    "lineWidth": 1,
                    "pointSize": 5,
                    "scaleDistribution": {
                      "type": "linear"
                    },
                    "showPoints": "auto",
                    "spanNulls": false,
                    "stacking": {
                      "group": "A",
                      "mode": "none"
                    },
                    "thresholdsStyle": {
                      "mode": "off"
                    }
                  },
                  "mappings": [],
                  "thresholds": {
                    "mode": "absolute",
                    "steps": [
                      {
                        "color": "green",
                        "value": 0
                      }
                    ]
                  },
                  "unit": "jobs/s"
                },
                "overrides": []
              },
              "gridPos": {
                "h": 8,
                "w": 12,
                "x": 0,
                "y": 245
              },
              "id": 17,
              "options": {
                "legend": {
                  "calcs": [],
                  "displayMode": "list",
                  "placement": "bottom",
                  "showLegend": true
                },
                "tooltip": {
                  "hideZeros": false,
                  "mode": "multi",
                  "sort": "none"
                }
              },
              "pluginVersion": "12.2.0-16791878397",
              "targets": [
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_job_watcher_cleanups_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "Cleanups",
                  "range": true,
                  "refId": "A"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_job_watcher_jobs_stalled_without_pod_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "Stalled without pod",
                  "range": true,
                  "refId": "B"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_job_watcher_jobs_finished_without_pod_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "Finished without pod",
                  "range": true,
                  "refId": "C"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_job_watcher_jobs_failed_on_buildkite_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "Reported failed",
                  "range": true,
                  "refId": "D"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_job_watcher_job_fail_on_buildkite_errors_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "Error reporting failure",
                  "range": true,
                  "refId": "E"
                }
              ],
              "title": "Job watcher job rate",
              "type": "timeseries"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "description": "",
              "fieldConfig": {
                "defaults": {
                  "color": {
                    "mode": "palette-classic"
                  },
                  "custom": {
                    "axisBorderShow": false,
                    "axisCenteredZero": false,
                    "axisColorMode": "text",
                    "axisLabel": "",
                    "axisPlacement": "auto",
                    "barAlignment": 0,
                    "barWidthFactor": 0.6,
                    "drawStyle": "line",
                    "fillOpacity": 0,
                    "gradientMode": "none",
                    "hideFrom": {
                      "legend": false,
                      "tooltip": false,
                      "viz": false
                    },
                    "insertNulls": false,
                    "lineInterpolation": "linear",
                    "lineWidth": 1,
                    "pointSize": 5,
                    "scaleDistribution": {
                      "type": "linear"
                    },
                    "showPoints": "auto",
                    "spanNulls": false,
                    "stacking": {
                      "group": "A",
                      "mode": "none"
                    },
                    "thresholdsStyle": {
                      "mode": "off"
                    }
                  },
                  "mappings": [],
                  "thresholds": {
                    "mode": "absolute",
                    "steps": [
                      {
                        "color": "green",
                        "value": 0
                      }
                    ]
                  }
                },
                "overrides": []
              },
              "gridPos": {
                "h": 8,
                "w": 12,
                "x": 12,
                "y": 245
              },
              "id": 19,
              "options": {
                "legend": {
                  "calcs": [],
                  "displayMode": "list",
                  "placement": "bottom",
                  "showLegend": true
                },
                "tooltip": {
                  "hideZeros": false,
                  "mode": "multi",
                  "sort": "none"
                }
              },
              "pluginVersion": "12.2.0-16791878397",
              "targets": [
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(buildkite_job_watcher_num_ignored_jobs{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
                  "hide": false,
                  "legendFormat": "Jobs ignored",
                  "range": true,
                  "refId": "A"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(buildkite_job_watcher_num_stalling_jobs{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
                  "hide": false,
                  "legendFormat": "Jobs stalling",
                  "range": true,
                  "refId": "B"
                }
              ],
              "title": "Job watcher gauges",
              "type": "timeseries"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "description": "The job watcher is an auxiliary component that watches for certain Kubernetes job conditions, such as stalling or finishing without ever starting a pod.",
              "fieldConfig": {
                "defaults": {
                  "color": {
                    "mode": "palette-classic"
                  },
                  "custom": {
                    "axisBorderShow": false,
                    "axisCenteredZero": false,
                    "axisColorMode": "text",
                    "axisLabel": "",
                    "axisPlacement": "auto",
                    "barAlignment": 0,
                    "barWidthFactor": 0.6,
                    "drawStyle": "line",
                    "fillOpacity": 0,
                    "gradientMode": "none",
                    "hideFrom": {
                      "legend": false,
                      "tooltip": false,
                      "viz": false
                    },
                    "insertNulls": false,
                    "lineInterpolation": "linear",
                    "lineWidth": 1,
                    "pointSize": 5,
                    "scaleDistribution": {
                      "type": "linear"
                    },
                    "showPoints": "auto",
                    "spanNulls": false,
                    "stacking": {
                      "group": "A",
                      "mode": "none"
                    },
                    "thresholdsStyle": {
                      "mode": "off"
                    }
                  },
                  "mappings": [],
                  "thresholds": {
                    "mode": "absolute",
                    "steps": [
                      {
                        "color": "green",
                        "value": 0
                      }
                    ]
                  },
                  "unit": "eps"
                },
                "overrides": []
              },
              "gridPos": {
                "h": 8,
                "w": 12,
                "x": 0,
                "y": 253
              },
              "id": 18,
              "options": {
                "legend": {
                  "calcs": [],
                  "displayMode": "list",
                  "placement": "bottom",
                  "showLegend": true
                },
                "tooltip": {
                  "hideZeros": false,
                  "mode": "multi",
                  "sort": "none"
                }
              },
              "pluginVersion": "12.2.0-16791878397",
              "targets": [
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_job_watcher_onadd_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "OnAdd events",
                  "range": true,
                  "refId": "E"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_job_watcher_ondelete_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "OnDelete events",
                  "range": true,
                  "refId": "A"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_job_watcher_onupdate_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "OnUpdate events",
                  "range": true,
                  "refId": "B"
                }
              ],
              "title": "Job watcher informer event rate",
              "type": "timeseries"
            }
          ],
          "title": "Job watcher",
          "type": "row"
        },
        {
          "collapsed": true,
          "gridPos": {
            "h": 1,
            "w": 24,
            "x": 0,
            "y": 76
          },
          "id": 22,
          "panels": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "description": "The pod watcher is an auxiliary component that watches for certain Kubernetes pod conditions, such as failing to pull an image.",
              "fieldConfig": {
                "defaults": {
                  "color": {
                    "mode": "palette-classic"
                  },
                  "custom": {
                    "axisBorderShow": false,
                    "axisCenteredZero": false,
                    "axisColorMode": "text",
                    "axisLabel": "",
                    "axisPlacement": "auto",
                    "barAlignment": 0,
                    "barWidthFactor": 0.6,
                    "drawStyle": "line",
                    "fillOpacity": 0,
                    "gradientMode": "none",
                    "hideFrom": {
                      "legend": false,
                      "tooltip": false,
                      "viz": false
                    },
                    "insertNulls": false,
                    "lineInterpolation": "linear",
                    "lineWidth": 1,
                    "pointSize": 5,
                    "scaleDistribution": {
                      "type": "linear"
                    },
                    "showPoints": "auto",
                    "spanNulls": false,
                    "stacking": {
                      "group": "A",
                      "mode": "none"
                    },
                    "thresholdsStyle": {
                      "mode": "off"
                    }
                  },
                  "mappings": [],
                  "thresholds": {
                    "mode": "absolute",
                    "steps": [
                      {
                        "color": "green",
                        "value": 0
                      }
                    ]
                  },
                  "unit": "jobs/s"
                },
                "overrides": []
              },
              "gridPos": {
                "h": 8,
                "w": 12,
                "x": 0,
                "y": 101
              },
              "id": 23,
              "options": {
                "legend": {
                  "calcs": [],
                  "displayMode": "list",
                  "placement": "bottom",
                  "showLegend": true
                },
                "tooltip": {
                  "hideZeros": false,
                  "mode": "multi",
                  "sort": "none"
                }
              },
              "pluginVersion": "12.2.0-16791878397",
              "targets": [
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_pod_watcher_jobs_failed_on_buildkite_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "Reported failed",
                  "range": true,
                  "refId": "D"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_pod_watcher_job_fail_on_buildkite_errors_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "Error reporting failure",
                  "range": true,
                  "refId": "E"
                }
              ],
              "title": "Pod watcher job rate",
              "type": "timeseries"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "description": "",
              "fieldConfig": {
                "defaults": {
                  "color": {
                    "mode": "palette-classic"
                  },
                  "custom": {
                    "axisBorderShow": false,
                    "axisCenteredZero": false,
                    "axisColorMode": "text",
                    "axisLabel": "",
                    "axisPlacement": "auto",
                    "barAlignment": 0,
                    "barWidthFactor": 0.6,
                    "drawStyle": "line",
                    "fillOpacity": 0,
                    "gradientMode": "none",
                    "hideFrom": {
                      "legend": false,
                      "tooltip": false,
                      "viz": false
                    },
                    "insertNulls": false,
                    "lineInterpolation": "linear",
                    "lineWidth": 1,
                    "pointSize": 5,
                    "scaleDistribution": {
                      "type": "linear"
                    },
                    "showPoints": "auto",
                    "spanNulls": false,
                    "stacking": {
                      "group": "A",
                      "mode": "none"
                    },
                    "thresholdsStyle": {
                      "mode": "off"
                    }
                  },
                  "mappings": [],
                  "thresholds": {
                    "mode": "absolute",
                    "steps": [
                      {
                        "color": "green",
                        "value": 0
                      }
                    ]
                  }
                },
                "overrides": []
              },
              "gridPos": {
                "h": 8,
                "w": 12,
                "x": 12,
                "y": 101
              },
              "id": 24,
              "options": {
                "legend": {
                  "calcs": [],
                  "displayMode": "list",
                  "placement": "bottom",
                  "showLegend": true
                },
                "tooltip": {
                  "hideZeros": false,
                  "mode": "multi",
                  "sort": "none"
                }
              },
              "pluginVersion": "12.2.0-16791878397",
              "targets": [
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(buildkite_pod_watcher_num_ignored_jobs{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
                  "hide": false,
                  "legendFormat": "Jobs ignored",
                  "range": true,
                  "refId": "A"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(buildkite_pod_watcher_num_job_cancel_checkers{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
                  "hide": false,
                  "legendFormat": "Cancellation checkers running",
                  "range": true,
                  "refId": "B"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(buildkite_pod_watcher_num_watching_for_image_failure{namespace=~\"$NAMESPACE\", pod=~\"$POD\"})",
                  "hide": false,
                  "legendFormat": "Image failure watchers running",
                  "range": true,
                  "refId": "C"
                }
              ],
              "title": "Pod watcher gauges",
              "type": "timeseries"
            },
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "description": "",
              "fieldConfig": {
                "defaults": {
                  "color": {
                    "mode": "palette-classic"
                  },
                  "custom": {
                    "axisBorderShow": false,
                    "axisCenteredZero": false,
                    "axisColorMode": "text",
                    "axisLabel": "",
                    "axisPlacement": "auto",
                    "barAlignment": 0,
                    "barWidthFactor": 0.6,
                    "drawStyle": "line",
                    "fillOpacity": 0,
                    "gradientMode": "none",
                    "hideFrom": {
                      "legend": false,
                      "tooltip": false,
                      "viz": false
                    },
                    "insertNulls": false,
                    "lineInterpolation": "linear",
                    "lineWidth": 1,
                    "pointSize": 5,
                    "scaleDistribution": {
                      "type": "linear"
                    },
                    "showPoints": "auto",
                    "spanNulls": false,
                    "stacking": {
                      "group": "A",
                      "mode": "none"
                    },
                    "thresholdsStyle": {
                      "mode": "off"
                    }
                  },
                  "mappings": [],
                  "thresholds": {
                    "mode": "absolute",
                    "steps": [
                      {
                        "color": "green",
                        "value": 0
                      }
                    ]
                  },
                  "unit": "eps"
                },
                "overrides": []
              },
              "gridPos": {
                "h": 8,
                "w": 12,
                "x": 0,
                "y": 109
              },
              "id": 25,
              "options": {
                "legend": {
                  "calcs": [],
                  "displayMode": "list",
                  "placement": "bottom",
                  "showLegend": true
                },
                "tooltip": {
                  "hideZeros": false,
                  "mode": "multi",
                  "sort": "none"
                }
              },
              "pluginVersion": "12.2.0-16791878397",
              "targets": [
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_pod_watcher_onadd_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "OnAdd events",
                  "range": true,
                  "refId": "E"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_pod_watcher_ondelete_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "OnDelete events",
                  "range": true,
                  "refId": "A"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_pod_watcher_onupdate_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "OnUpdate events",
                  "range": true,
                  "refId": "B"
                }
              ],
              "title": "Pod watcher informer event rate",
              "type": "timeseries"
            }
          ],
          "title": "Pod watcher",
          "type": "row"
        },
        {
          "collapsed": true,
          "gridPos": {
            "h": 1,
            "w": 24,
            "x": 0,
            "y": 77
          },
          "id": 28,
          "panels": [
            {
              "datasource": {
                "type": "prometheus",
                "uid": "prometheus"
              },
              "description": "The completion watcher is an auxiliary stack component that watches pods for certain completion conditions in order to effectively clean up, e.g. to prevent a sidecar container from causing a pod to live forever.",
              "fieldConfig": {
                "defaults": {
                  "color": {
                    "mode": "palette-classic"
                  },
                  "custom": {
                    "axisBorderShow": false,
                    "axisCenteredZero": false,
                    "axisColorMode": "text",
                    "axisLabel": "",
                    "axisPlacement": "auto",
                    "barAlignment": 0,
                    "barWidthFactor": 0.6,
                    "drawStyle": "line",
                    "fillOpacity": 0,
                    "gradientMode": "none",
                    "hideFrom": {
                      "legend": false,
                      "tooltip": false,
                      "viz": false
                    },
                    "insertNulls": false,
                    "lineInterpolation": "linear",
                    "lineWidth": 1,
                    "pointSize": 5,
                    "scaleDistribution": {
                      "type": "linear"
                    },
                    "showPoints": "auto",
                    "spanNulls": false,
                    "stacking": {
                      "group": "A",
                      "mode": "none"
                    },
                    "thresholdsStyle": {
                      "mode": "off"
                    }
                  },
                  "mappings": [],
                  "thresholds": {
                    "mode": "absolute",
                    "steps": [
                      {
                        "color": "green",
                        "value": 0
                      }
                    ]
                  },
                  "unit": "eps"
                },
                "overrides": []
              },
              "gridPos": {
                "h": 8,
                "w": 12,
                "x": 0,
                "y": 102
              },
              "id": 27,
              "options": {
                "legend": {
                  "calcs": [],
                  "displayMode": "list",
                  "placement": "bottom",
                  "showLegend": true
                },
                "tooltip": {
                  "hideZeros": false,
                  "mode": "multi",
                  "sort": "none"
                }
              },
              "pluginVersion": "12.2.0-16791878397",
              "targets": [
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_completion_watcher_cleanups_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "Cleanups",
                  "range": true,
                  "refId": "A"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_completion_watcher_onadd_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "OnAdd events",
                  "range": true,
                  "refId": "E"
                },
                {
                  "datasource": {
                    "type": "prometheus",
                    "uid": "prometheus"
                  },
                  "editorMode": "builder",
                  "expr": "sum(rate(buildkite_completion_watcher_onupdate_events_total{namespace=~\"$NAMESPACE\", pod=~\"$POD\"}[$__rate_interval]))",
                  "hide": false,
                  "legendFormat": "OnUpdate events",
                  "range": true,
                  "refId": "B"
                }
              ],
              "title": "Completion watcher event rate",
              "type": "timeseries"
            }
          ],
          "title": "Completion watcher",
          "type": "row"
        }
      ],
      "schemaVersion": 41,
      "tags": [
        "buildkite",
        "ci/cd"
      ],
      "templating": {
        "list": [
          {
            "current": {},
            "datasource": {
              "type": "prometheus",
              "uid": "prometheus"
            },
            "definition": "label_values(buildkite_monitor_job_queries_total,namespace)",
            "description": "",
            "includeAll": true,
            "label": "Namespace",
            "multi": true,
            "name": "NAMESPACE",
            "options": [],
            "query": {
              "qryType": 1,
              "query": "label_values(buildkite_monitor_job_queries_total,namespace)",
              "refId": "PrometheusVariableQueryEditor-VariableQuery"
            },
            "refresh": 1,
            "regex": "",
            "type": "query"
          },
          {
            "current": {},
            "datasource": {
              "type": "prometheus",
              "uid": "prometheus"
            },
            "definition": "label_values(buildkite_monitor_job_queries_total{namespace=~\"$NAMESPACE\"},pod)",
            "description": "",
            "includeAll": true,
            "label": "Controller pod",
            "multi": true,
            "name": "POD",
            "options": [],
            "query": {
              "qryType": 1,
              "query": "label_values(buildkite_monitor_job_queries_total{namespace=~\"$NAMESPACE\"},pod)",
              "refId": "PrometheusVariableQueryEditor-VariableQuery"
            },
            "refresh": 1,
            "regex": "",
            "type": "query"
          }
        ]
      },
      "time": {
        "from": "now-1h",
        "to": "now"
      },
      "timepicker": {},
      "timezone": "browser",
      "title": "Buildkite Agent Stack for Kubernetes",
      "uid": "a0e606b4-cd86-455a-9be5-80d1653b982c",
      "version": 69,
      "weekStart": ""
    }
{{end}}