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

{{/* Values the tools' charts take separately but must agree on. */}}
{{- define "dynamo.checks" -}}
{{- if .Values.objectStore.enabled -}}
{{- range $t := list "stator" "armature" "exciter" -}}
{{- $s := index $.Values $t "secrets" -}}
{{- if or (ne $s.s3AccessKey $.Values.objectStore.accessKey) (ne $s.s3SecretKey $.Values.objectStore.secretKey) -}}
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
