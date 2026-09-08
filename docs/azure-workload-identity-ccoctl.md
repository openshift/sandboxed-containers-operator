# Azure Workload Identity Workflow Using ccoctl

This guide describes how to set up Azure Workload Identity credentials for the
sandboxed-containers-operator using `ccoctl`. This is an alternative to the automatic
[workload identity workflow](credentials-handling.md) and is suited for restricted environments
where the operator cannot create managed identities itself.

## How It Works

1. You run `ccoctl` locally to create a managed identity, federated credential, and generate a `cco-secret` manifest
2. You apply the generated `cco-secret` to the cluster
3. The operator's credentials controller detects `cco-secret` and automatically converts it into
   `peer-pods-secret` with the correct environment variable format (`AZURE_CLIENT_ID`,
   `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID`, `AZURE_FEDERATED_TOKEN_FILE`)
4. You install the operator and create a KataConfig — `peer-pods-secret` already exists, so no
   env vars are needed in the Subscription

## Prerequisites

- Azure CLI installed and configured with permissions to create identities and assign roles
- `oc` CLI authenticated to your OpenShift cluster
- OpenShift cluster with OIDC issuer configured (standard for Azure ARO clusters)

## Installing ccoctl

The `ccoctl` binary ships inside the Cloud Credential Operator pod on your cluster:

```bash
CCO_POD=$(oc get pods -n openshift-cloud-credential-operator \
  -l app=cloud-credential-operator \
  -o jsonpath='{.items[0].metadata.name}')

oc cp -n openshift-cloud-credential-operator \
  "${CCO_POD}:/usr/bin/ccoctl" ./ccoctl

chmod +x ./ccoctl
./ccoctl --help
```

## Credentials Setup

### 1. Collect environment info

```bash
# Extract cluster configuration from Azure cloud-conf ConfigMap
AZURE_CLOUD_CONFIG=$(oc get configmap cloud-conf \
  -n openshift-cloud-controller-manager \
  -o jsonpath='{.data.cloud\.conf}')

SUBSCRIPTION_ID=$(echo "${AZURE_CLOUD_CONFIG}" | grep -o '"subscriptionId":"[^"]*"' | cut -d'"' -f4)
RESOURCE_GROUP=$(echo "${AZURE_CLOUD_CONFIG}" | grep -o '"resourceGroup":"[^"]*"' | cut -d'"' -f4)
LOCATION=$(echo "${AZURE_CLOUD_CONFIG}" | grep -o '"location":"[^"]*"' | cut -d'"' -f4)

# Get the OIDC issuer URL from cluster
ISSUER=$(oc get authentication.config.openshift.io cluster -o jsonpath='{.spec.serviceAccountIssuer}')

# Extract cluster name and OIDC resource group from the OIDC issuer
STORAGE_ACCOUNT=$(echo $ISSUER | sed 's|https://||' | cut -d'.' -f1)
OIDC_RESOURCE_GROUP=$(az storage account show --name $STORAGE_ACCOUNT --query resourceGroup -o tsv)
CLUSTER_NAME=$(echo $OIDC_RESOURCE_GROUP | sed 's/-oidc$//')

echo "Cluster Name:        ${CLUSTER_NAME}"
echo "Resource Group:      ${RESOURCE_GROUP}"
echo "Location:            ${LOCATION}"
echo "Subscription:        ${SUBSCRIPTION_ID}"
echo "OIDC Issuer:         ${ISSUER}"
echo "OIDC Resource Group: ${OIDC_RESOURCE_GROUP}"
```

### 2. Fetch the CredentialsRequest from the operator image

The committed CredentialsRequest is bundled inside the operator image under
`/config/peerpods/credentials-requests/ccoctl/`. Copy it out:

```bash
mkdir -p credrequests

OPERATOR_POD=$(oc get pods -n openshift-sandboxed-containers-operator \
  -l control-plane=controller-manager \
  -o jsonpath='{.items[0].metadata.name}')

oc cp -n openshift-sandboxed-containers-operator \
  "${OPERATOR_POD}:/config/peerpods/credentials-requests/ccoctl/credentials_request_azure_wif.yaml" \
  credrequests/credentials_request_azure_wif.yaml
```

### 3. Run ccoctl to create the managed identity and generate the secret manifest

```bash
./ccoctl azure create-managed-identities \
  --name="${CLUSTER_NAME}" \
  --region="${LOCATION}" \
  --subscription-id="${SUBSCRIPTION_ID}" \
  --credentials-requests-dir=./credrequests \
  --output-dir=./ccoctl-output \
  --issuer-url="${ISSUER}" \
  --oidc-resource-group-name="${OIDC_RESOURCE_GROUP}" \
  --installation-resource-group-name="${RESOURCE_GROUP}"
```

This command will:
- Create a managed identity named `${CLUSTER_NAME}-openshift-sandboxed-containers-operator-cco-secret` in the OIDC resource group
- Assign all required roles scoped to the installation resource group
- Create a federated identity credential linked to the service account `system:serviceaccount:openshift-sandboxed-containers-operator:default`
- Generate a secret manifest at `./ccoctl-output/manifests/openshift-sandboxed-containers-operator-cco-secret-credentials.yaml`

### 4. Apply the generated secret

```bash
oc apply -f ./ccoctl-output/manifests/openshift-sandboxed-containers-operator-cco-secret-credentials.yaml
```

The credentials controller will detect `cco-secret`, parse the Azure format, and
automatically create `peer-pods-secret` with:

```
AZURE_CLIENT_ID=<client ID from managed identity>
AZURE_TENANT_ID=<tenant ID>
AZURE_SUBSCRIPTION_ID=<subscription ID>
AZURE_FEDERATED_TOKEN_FILE=/var/run/secrets/openshift/serviceaccount/token
```

### 5. Verify peer-pods-secret was created

```bash
oc get secret peer-pods-secret -n openshift-sandboxed-containers-operator -o yaml
```

The secret should contain `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID`, and
`AZURE_FEDERATED_TOKEN_FILE` keys and carry the label
`kataconfiguration.openshift.io/credentials-request-based: "true"`.

### 6. Continue with OSC installation

Install the operator and create a KataConfig with `enablePeerPods: true`. Because `peer-pods-secret`
already exists, no credential env vars are required in the Subscription.

## Reducing Permissions After Image Creation

The CredentialsRequest bundled in the operator image
(`credentials_request_azure_wif.yaml`) includes extended role assignments required
for the podvm image creation job (Storage Account Contributor, Compute Gallery Artifacts Publisher).
Once image creation has completed successfully these roles are no longer needed and can be removed
to follow the principle of least privilege.

To reduce permissions, create a trimmed CredentialsRequest that contains only the roles required
by the cloud-api-adaptor at runtime, then re-run `ccoctl` to update the role assignments in place.
The managed identity client ID does not change, so the existing `cco-secret` and `peer-pods-secret`
remain valid — no re-apply is needed.

```bash
# Create a reduced CredentialsRequest (runtime roles only, no image creation roles)
# Note: This will override credentials_request_azure_wif.yaml due to alphabetical ordering
cat > credrequests/credentials_request_azure_wif_minimal.yaml <<'EOF'
apiVersion: cloudcredential.openshift.io/v1
kind: CredentialsRequest
metadata:
  name: openshift-sandboxed-containers-azure
  namespace: openshift-cloud-credential-operator
spec:
  secretRef:
    name: cco-secret
    namespace: openshift-sandboxed-containers-operator
  serviceAccountNames:
    - default
  providerSpec:
    apiVersion: cloudcredential.openshift.io/v1
    kind: AzureProviderSpec
    roleBindings:
      - role: Reader
      - role: Virtual Machine Contributor
      - role: Network Contributor
EOF

# Re-run ccoctl — updates the role assignments without recreating the identity or secret
./ccoctl azure create-managed-identities \
  --name="${CLUSTER_NAME}" \
  --region="${LOCATION}" \
  --subscription-id="${SUBSCRIPTION_ID}" \
  --credentials-requests-dir=./credrequests \
  --output-dir=./ccoctl-output \
  --issuer-url="${ISSUER}" \
  --oidc-resource-group-name="${OIDC_RESOURCE_GROUP}" \
  --installation-resource-group-name="${RESOURCE_GROUP}"
```

> **Note:** If you need to create a new podvm image in the future, re-run `ccoctl` with the full
> `credentials_request_azure_wif.yaml` (fetched from the operator image) to restore
> the extended permissions, then reduce them again once image creation completes.

## Cleanup

Delete `cco-secret` first (this cascades to `peer-pods-secret` via owner reference), then remove
the managed identity created by `ccoctl`:

```bash
oc delete secret cco-secret -n openshift-sandboxed-containers-operator

# Delete the managed identity (this also deletes federated credentials and role assignments)
MANAGED_IDENTITY_NAME="${CLUSTER_NAME}-openshift-sandboxed-containers-operator-cco-secret"
az identity delete --name "${MANAGED_IDENTITY_NAME}" --resource-group "${OIDC_RESOURCE_GROUP}"
```
