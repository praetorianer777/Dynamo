{{/* A tool's address, as its own chart makes it from ingress.host and TLS (on unless turned off). */}}
{{- define "dynamo.toolURL" -}}
{{- $v := index .root.Values .tool -}}
{{- $tls := dig "ingress" "tls" "enabled" true $v -}}
{{- printf "%s://%s" (ternary "https" "http" $tls) (required (printf "Set %s.ingress.host, the address people open %s at." .tool .tool) (dig "ingress" "host" "" $v)) -}}
{{- end -}}

{{- define "dynamo.keycloakURL" -}}
{{- $k := .Values.keycloak -}}
{{- if $k.url -}}{{- trimSuffix "/" $k.url -}}
{{- else if $k.ingress.enabled -}}{{- printf "%s://%s" (ternary "https" "http" $k.ingress.tls.enabled) $k.ingress.host -}}
{{- else -}}http://keycloak:8080{{- end -}}
{{- end -}}

{{- define "dynamo.issuer" -}}
{{- if .Values.oidc.issuer -}}{{- .Values.oidc.issuer -}}
{{- else if .Values.keycloak.enabled -}}{{- printf "%s/realms/dynamo" (include "dynamo.keycloakURL" .) -}}
{{- else -}}{{- fail "Set oidc.issuer, the realm the three tools sign in through, or turn keycloak.enabled on." -}}
{{- end -}}
{{- end -}}

{{- define "dynamo.labels" -}}
app.kubernetes.io/part-of: dynamo
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{/* A password kept across upgrades: the one in the Secret, else the value, else a new one. */}}
{{- define "dynamo.password" -}}
{{- $old := lookup "v1" "Secret" .root.Release.Namespace .secret -}}
{{- if and $old (index $old.data .key) -}}{{- index $old.data .key | b64dec -}}
{{- else if .value -}}{{- .value -}}
{{- else -}}{{- randAlphaNum 32 -}}{{- end -}}
{{- end -}}

{{/* The Secrets the suite's own secrets are read from: one made beforehand when named, else the chart's. */}}
{{- define "dynamo.oidcSecret" -}}
{{- .Values.oidc.existingSecret | default (printf "%s-connect" .Release.Name) -}}
{{- end -}}

{{- define "dynamo.keycloakAdminSecret" -}}
{{- .Values.keycloak.admin.existingSecret | default (printf "%s-keycloak-admin" .Release.Name) -}}
{{- end -}}

{{- define "dynamo.armatureAdminSecret" -}}
{{- .Values.armatureAdmin.existingSecret | default (printf "%s-armature-admin" .Release.Name) -}}
{{- end -}}

{{- define "dynamo.objectStoreSecret" -}}
{{- .Values.objectStore.existingSecret | default (printf "%s-object-store" .Release.Name) -}}
{{- end -}}

{{/* The Secret holding a tool's administrator, which the connect Job and helm test sign in with. */}}
{{- define "dynamo.adminSecret" -}}
{{- if eq .tool "armature" -}}{{- include "dynamo.armatureAdminSecret" .root -}}
{{- else -}}{{- printf "%s-bootstrap-admin" .tool -}}{{- end -}}
{{- end -}}

{{/* Values the tools' charts take separately but must agree on. */}}
{{- define "dynamo.checks" -}}
{{- if .Values.objectStore.enabled -}}
{{- $o := .Values.objectStore -}}
{{- range $t := list "stator" "armature" "exciter" -}}
{{- $s := index $.Values $t "secrets" -}}
{{- if $o.existingSecret -}}
{{- if or (ne (toString $s.create) "false") (ne $s.existingSecret $o.existingSecret) -}}
{{- fail (printf "Set %s.secrets.create to false and %s.secrets.existingSecret to %s, objectStore.existingSecret, or %s's uploads are refused by the store." $t $t $o.existingSecret $t) -}}
{{- end -}}
{{- else if $s.existingSecret -}}
{{- fail (printf "%s.secrets.existingSecret names a Secret the bundled store does not read; set objectStore.existingSecret to the same Secret, or turn objectStore.enabled off for an S3 of your own." $t) -}}
{{- else if or (ne $s.s3AccessKey $o.accessKey) (ne $s.s3SecretKey $o.secretKey) -}}
{{- fail (printf "%s.secrets.s3AccessKey and s3SecretKey must be objectStore.accessKey and secretKey, or %s's uploads are refused by the store." $t $t) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- range $t := list "stator" "exciter" -}}
{{- if ne (toJson (index $.Values $t "bootstrap" "members")) (toJson $.Values.members) -}}
{{- fail (printf "%s.bootstrap.members must be the same list as members, so the same people are let into all three." $t) -}}
{{- end -}}
{{- end -}}
{{- end -}}
