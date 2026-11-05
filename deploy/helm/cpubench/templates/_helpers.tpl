{{- define "cpubench.name" -}}cpubench-{{ .Values.mode }}-{{ required "set arch to amd64 or arm64" .Values.arch }}{{- end -}}
{{- define "cpubench.env" -}}
- {name: NODE_NAME, valueFrom: {fieldRef: {fieldPath: spec.nodeName}}}
- {name: POD_NAME, valueFrom: {fieldRef: {fieldPath: metadata.name}}}
- {name: POD_NAMESPACE, valueFrom: {fieldRef: {fieldPath: metadata.namespace}}}
- {name: HOST_IP, valueFrom: {fieldRef: {fieldPath: status.hostIP}}}
- {name: CPUBENCH_TOKEN, valueFrom: {secretKeyRef: {name: {{ .Values.tokenSecret }}, key: token, optional: true}}}
- {name: CPUBENCH_BMC_PASSWORD, valueFrom: {secretKeyRef: {name: {{ .Values.tokenSecret }}, key: bmcPassword, optional: true}}}
{{- end -}}
{{- define "cpubench.placement" -}}
nodeSelector:
  kubernetes.io/arch: {{ required "set arch to amd64 or arm64" .Values.arch }}
  {{- with .Values.nodeName }}
  kubernetes.io/hostname: {{ . }}
  {{- end }}
{{- end -}}
{{- define "cpubench.resources" -}}
requests: {cpu: {{ .Values.cpu | quote }}, memory: {{ .Values.memory }}}
limits: {cpu: {{ .Values.cpu | quote }}, memory: {{ .Values.memory }}}
{{- end -}}
