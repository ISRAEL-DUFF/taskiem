{{/* Names and labels shared by every template. */}}

{{- define "taskiem.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "taskiem.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "taskiem.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "taskiem.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "taskiem.selectorLabels" -}}
app.kubernetes.io/name: {{ include "taskiem.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "taskiem.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "taskiem.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "taskiem.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{/* The roles this release runs: every split role, or just "all". */}}
{{- define "taskiem.roles" -}}
{{- if eq .Values.mode "all" -}}
all
{{- else if eq .Values.mode "split" -}}
api edge orchestrator scheduler worker
{{- else -}}
{{- fail (printf "mode must be split or all, not %q" .Values.mode) -}}
{{- end -}}
{{- end -}}

{{- define "taskiem.storageClaim" -}}
{{- default (printf "%s-data" (include "taskiem.fullname" .)) .Values.storage.existingClaim -}}
{{- end -}}

{{/* Pod and container security: distroless nonroot, nothing writable but emptyDirs. */}}
{{- define "taskiem.podSecurityContext" -}}
runAsNonRoot: true
runAsUser: 65532
runAsGroup: 65532
fsGroup: 65532
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{- define "taskiem.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: [ALL]
{{- end -}}

{{/*
Graceful shutdown (docs/reliability.md#graceful-shutdown): readiness flips on
SIGTERM, the pod keeps serving for delaySeconds while load balancers drop it,
then workers drain for up to workerDrainSeconds and release what they hold.
All of it must fit in the grace period, with room to release leases.
*/}}
{{- define "taskiem.gracePeriod" -}}
{{- $s := .Values.shutdown -}}
{{- $need := add $s.delaySeconds $s.workerDrainSeconds 10 -}}
{{- if gt (int $need) (int $s.gracePeriodSeconds) -}}
{{- fail (printf "shutdown.gracePeriodSeconds (%v) must be at least delaySeconds + workerDrainSeconds + 10 (%v)" $s.gracePeriodSeconds $need) -}}
{{- end -}}
{{- $s.gracePeriodSeconds -}}
{{- end -}}

{{/*
The scheduler's replicas. Its claims are safe to run twice (SKIP LOCKED,
leases, conditional updates), but each scheduler archives runs and writes
audit anchors to its own local directory, and the default claim is
ReadWriteOnce under a Recreate strategy: more than one needs the S3 archive
and no volume, and then has no audit anchoring (docs/kubernetes.md#storage).
*/}}
{{- define "taskiem.schedulerReplicas" -}}
{{- $n := int .Values.roles.scheduler.replicas -}}
{{- if and (gt $n 1) (or .Values.storage.enabled (not (index .Values.config "TASKIEM_ARCHIVE_S3_BUCKET"))) -}}
{{- fail "roles.scheduler.replicas above 1 needs the S3 archive (config.TASKIEM_ARCHIVE_S3_BUCKET) and storage.enabled: false; each scheduler would otherwise keep its own local archive, and the volume can be mounted once (docs/kubernetes.md#storage)" -}}
{{- end -}}
{{- $n -}}
{{- end -}}

{{- define "taskiem.shutdownEnv" -}}
- name: TASKIEM_SHUTDOWN_DELAY
  value: {{ printf "%vs" .Values.shutdown.delaySeconds | quote }}
- name: TASKIEM_WORKER_DRAIN
  value: {{ printf "%vs" .Values.shutdown.workerDrainSeconds | quote }}
{{- end -}}
