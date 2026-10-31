# OpenBao's configuration, rendered by templates/openbao.yaml with tpl.
ui = false

storage "raft" {
  path    = "/openbao/data"
  node_id = "openbao"
}

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true
}

api_addr     = "http://openbao:8200"
cluster_addr = "http://127.0.0.1:8201"

# Unseals itself on every start with the key from the Secret, so a restarted
# pod needs nobody.
seal "static" {
  current_key_id = "dynamo-1"
  current_key    = "env://DYNAMO_OPENBAO_UNSEAL_KEY"
}

# Every request and answer, secrets hashed, to the pod's log. OpenBao takes
# audit devices from its configuration only, and Rotor refuses one without.
audit "file" "stdout" {
  description = "Every request and answer, secrets hashed, in the pod's log"
  options {
    file_path = "stdout"
  }
}

# Runs once, on the first start against empty storage. A failed request
# leaves OpenBao refusing to unseal, so only the Kubernetes config may fail.
initialize "admin" {
  # Orphan, or it would die with the root token self-init revokes.
  request "token" {
    operation = "update"
    path      = "auth/token/create"
    data = {
      id = {
        eval_type       = "string"
        eval_source     = "env"
        env_var         = "DYNAMO_OPENBAO_ADMIN_TOKEN"
        require_present = true
      }
      policies     = ["root"]
      no_parent    = true
      display_name = "dynamo-admin"
    }
  }
}

initialize "kubernetes" {
  request "enable" {
    operation = "update"
    path      = "sys/auth/kubernetes"
    data      = { type = "kubernetes" }
  }
  # Rotor's configure Job writes this again, so a failure here is mended there.
  request "config" {
    operation     = "update"
    path          = "auth/kubernetes/config"
    allow_failure = true
    data          = { kubernetes_host = {{ .Values.openbao.kubernetesHost | quote }} }
  }
}
