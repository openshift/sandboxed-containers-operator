# Token-Based Credentials Setup Using ccoctl

This guide describes how to set up token-based credentials (Azure Workload Identity and AWS STS) for the sandboxed-containers-operator (for peer-pods usage) using `ccoctl`, this can be used with token based enabled cluster only.

## Overview

The `ccoctl` tool provides a standardized way to provision cloud credentials that work with OpenShift's token-based authentication:

- **Azure**: Creates managed identities with federated credentials (Workload Identity)
- **AWS**: Creates IAM roles with OIDC trust policies (STS/IRSA)

Both approaches follow the same pattern, once you have deployed the OSC operator catalog:
1. You run `ccoctl` locally to create cloud resources and generate a `cco-secret` manifest
2. You apply the generated `cco-secret` to the cluster
3. The operator's credentials controller detects `cco-secret` and automatically converts it into `peer-pods-secret` with the correct environment variables
4. You complete configuring OSC and create KataConfig with enablePeerPods=true.


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

---

## Azure Workload Identity Setup

### Prerequisites

- OpenShift cluster with OIDC issuer configured (Workload/Federated Identity mode) and OSC operator installed
- Azure CLI installed and configured with permissions to create identities and assign roles
- `oc` CLI authenticated to your OpenShift cluster

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
mkdir credrequests

OPERATOR_POD=$(oc get pods -n openshift-sandboxed-containers-operator \
  -l control-plane=controller-manager \
  -o jsonpath='{.items[0].metadata.name}')

oc cp -n openshift-sandboxed-containers-operator \
  "${OPERATOR_POD}:/config/peerpods/credentials-requests/ccoctl/credentials_request_azure_wif.yaml" \
  ./credrequests/credentials_request_azure_wif.yaml
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
- Create a federated identity credential linked to the service account
- Generate a secret manifest at `./ccoctl-output/manifests/openshift-sandboxed-containers-operator-cco-secret-credentials.yaml`

### 4. Apply the generated secret

```bash
oc apply -f ./ccoctl-output/manifests/openshift-sandboxed-containers-operator-cco-secret-credentials.yaml
```

The credentials controller will detect `cco-secret`, parse the Azure format, and
automatically create the populated `peer-pods-secret`

### 5. Verify peer-pods-secret was created

```bash
oc get secret peer-pods-secret -n openshift-sandboxed-containers-operator -o yaml
```

### 6. Continue with OSC installation

Complete configuration and create a KataConfig with `enablePeerPods: true`

### Reducing Permissions After Image Creation

The CredentialsRequest bundled in the operator image
(`credentials_request_azure_wif.yaml`) includes extended role assignments required
for the podvm image creation job (Storage Account Contributor, Compute Gallery Artifacts Publisher).
Once image creation has completed successfully these roles are no longer needed and can be removed
to follow the principle of least privilege.

To reduce permissions, create a trimmed CredentialsRequest that contains only the roles required
by the cloud-api-adaptor at runtime, then re-run `ccoctl` command from earlier to update the role assignments in place.

```bash
# Create a reduced CredentialsRequest (runtime roles only, no image creation roles)
# This removes everything after the #### marker (image creation permissions)
sed '/####/,$d' credrequests/credentials_request_azure_wif.yaml > \
  credrequests/credentials_request_azure_wif_minimal.yaml

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

### Cleanup

```bash
oc delete secret cco-secret -n openshift-sandboxed-containers-operator

./ccoctl azure delete \
  --name="${CLUSTER_NAME}" \
  --region="${LOCATION}" \
  --subscription-id="${SUBSCRIPTION_ID}" \
  --oidc-resource-group-name="${OIDC_RESOURCE_GROUP}" \
  --storage-account-name="dummystorageaccontname"
```

> **Note**: The dummy storage account name prevents deletion of the actual OIDC storage. For extra safety or if you prefer manual cleanup, delete only the managed identities (filtered by `openshift.io_cloud-credential-operator_${CLUSTER_NAME}: owned` tag) and custom roles using Azure CLI.

---

## AWS STS Setup

### Prerequisites

- OpenShift cluster with OIDC issuer configured (STS mode) and OSC operator installed
- AWS CLI installed and configured with IAM permissions to create roles and policies
- `oc` CLI authenticated to your OpenShift cluster

### 1. Collect environment info

```bash
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query "Account" --output text)
AWS_REGION=$(oc get infrastructure cluster -o jsonpath='{.status.platformStatus.aws.region}')
OIDC_PROVIDER=$(oc get authentication cluster -o jsonpath='{.spec.serviceAccountIssuer}' \
  | sed 's|https://||')
IDENTITY_PROVIDER_ARN="arn:aws:iam::${AWS_ACCOUNT_ID}:oidc-provider/${OIDC_PROVIDER}"

echo "Account:  ${AWS_ACCOUNT_ID}"
echo "Region:   ${AWS_REGION}"
echo "OIDC:     ${OIDC_PROVIDER}"
```

### 2. Fetch the CredentialsRequest from the operator image

The committed CredentialsRequest is bundled inside the operator image under
`/config/peerpods/credentials-requests/ccoctl/`. Copy it out:

```bash
mkdir credrequests

OPERATOR_POD=$(oc get pods -n openshift-sandboxed-containers-operator \
  -l control-plane=controller-manager \
  -o jsonpath='{.items[0].metadata.name}')

oc cp -n openshift-sandboxed-containers-operator \
  "${OPERATOR_POD}:/config/peerpods/credentials-requests/ccoctl/credentials_request_aws_sts.yaml" \
  ./credrequests/credentials_request_aws_sts.yaml
```

### 3. Run ccoctl to create the IAM role and generate the secret manifest

```bash
# Use some unique name
AWS_NAME="osc-peerpods-$(oc get clusterversion -o jsonpath='{.items[0].spec.clusterID}' | cut -c1-8)"

./ccoctl aws create-iam-roles \
  --name="${AWS_NAME}" \
  --region="${AWS_REGION}" \
  --credentials-requests-dir=./credrequests \
  --output-dir=./ccoctl-output \
  --identity-provider-arn="${IDENTITY_PROVIDER_ARN}"
```

This creates an IAM role and writes a `cco-secret` manifest to `./ccoctl-output/manifests/`.

### 4. Apply the generated secret

```bash
oc apply -f ./ccoctl-output/manifests/openshift-sandboxed-containers-operator-cco-secret-credentials.yaml
```

The credentials controller will detect `cco-secret`, parse the AWS format, and
automatically create a populated `peer-pods-secret`

### 5. Verify peer-pods-secret was created

```bash
oc get secret peer-pods-secret -n openshift-sandboxed-containers-operator -o yaml
```

### 6. Continue with OSC installation

Complete configuration and create a KataConfig with `enablePeerPods: true`

### Reducing Permissions After Image Creation

The CredentialsRequest bundled in the operator image (`credentials_request_aws_sts.yaml`) includes
extended permissions required for the podvm image creation job (S3 bucket management, VMImport IAM
role). Once image creation has completed successfully these permissions are no longer needed and
can be removed to follow the principle of least privilege.

To reduce permissions, create a trimmed CredentialsRequest that contains only permissions required
by the cloud-api-adaptor at runtime, then re-run `ccoctl` to update the IAM role policy
in place.

```bash
# Create a reduced CredentialsRequest
# This removes everything after the #### marker (image creation permissions)
sed '/####/,$d' credrequests/credentials_request_aws_sts.yaml > \
  credrequests/credentials_request_aws_sts_minimal.yaml

# Re-run ccoctl — updates the IAM role policy without recreating the role or secret
./ccoctl aws create-iam-roles \
  --name="${AWS_NAME}" \
  --region="${AWS_REGION}" \
  --credentials-requests-dir=./credrequests \
  --output-dir=./ccoctl-output \
  --identity-provider-arn="${IDENTITY_PROVIDER_ARN}"
```

> **Note:** If you need to create a new podvm image in the future, re-run `ccoctl` with the full
> `credentials_request_aws_sts.yaml` (fetched from the operator image) to restore the extended
> permissions, then reduce them again once image creation completes.

### Cleanup

```bash
oc delete secret cco-secret -n openshift-sandboxed-containers-operator

./ccoctl aws delete \
  --name="${AWS_NAME}" \
  --region="${AWS_REGION}"
```

> **Note**: AWS resources are tag-protected, so `ccoctl delete` only removes resources it created. For extra safety or if you prefer manual cleanup, delete only the IAM roles (filtered by `ccoctl.openshift.io/${AWS_NAME}: owned` tag) using AWS CLI.
