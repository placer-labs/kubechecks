# kubechecks-rbac

This chart deploys the Cluster Role and Cluster Role binding for the kubechecks running outside of existing cluster. 

It is not required if you're operating all within the same cluster.

## Secrets access

Secrets are granted with a Role and RoleBinding in `namespace` (the Argo CD namespace), not cluster-wide. kubechecks only reads repository and cluster credentials from there. Before 0.5.0 the ClusterRole granted read access to Secrets in every namespace; set `clusterWideSecrets: true` to keep that behaviour.
