# Peer-Pods Credentials Handling

This document describes the three credential flows available for providing cloud credentials to the OpenShift Sandboxed Containers Operator for peer-pods functionality.

## Overview

The operator supports three distinct flows for obtaining cloud credentials, evaluated in priority order:

1. **User-created secret** (highest priority)
2. **STS workflow** (Workload identity via federated tokens)
3. **CCO workflow** (Cloud Credential Operator - fallback)

The credential flow selection happens automatically in `setupPeerPodsCredentials()` during KataConfig creation or update when `enablePeerPods: true`.

## Flow 1: User-Created Secret (Manual)

**Priority**: Highest

**Supported Providers**: All

### Description
Users manually create the `peer-pods-secret` in the `openshift-sandboxed-containers-operator` namespace with cloud provider credentials.

### User Guide
#### Credentials Setup
1. At configuration time (before deploying KataConfig), create `peer-pods-secret` with cloud credentials as follows:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: peer-pods-secret
  namespace: openshift-sandboxed-containers-operator
type: Opaque
data:
  # Azure example
  AZURE_CLIENT_ID: <base64-encoded-client-id>
  AZURE_CLIENT_SECRET: <base64-encoded-client-secret>
  AZURE_TENANT_ID: <base64-encoded-tenant-id>
  AZURE_SUBSCRIPTION_ID: <base64-encoded-subscription-id>
```

#### Credentials Cleanup
1. Delete the secret when no longer needed:
```
oc delete secret/peer-pods-secret -n openshift-sandboxed-containers-operator
```


### How it works
1. User creates `peer-pods-secret` with required cloud credentials
2. Operator detects the existing secret and skips automated credential setup
3. Secret persists until manually deleted by the user

---

## Flow 2: STS Workflow (Workload Identity)

**Priority**: Second (if user's secret was not created)

**Supported Providers**: Azure

### Description
When the cluster is in workload/federated identitiy mode and an identity was provided during OLM installation (when applying the subscription), the operator uses Azure Workload Identity (federated tokens) for authentication without long-lived secrets.

The operator reads environment variables set during OLM installation and creates a secret pointing to the projected service account token.

Reference: [OpenShift Enhancement Proposal #1800](https://github.com/openshift/enhancements/pull/1800)

### User Guide
#### Credentials Setup
1. Create an identity within your RG
```
IDENTITY_NAME=<identity name>
RESOURCE_GROUP=<the RG you have created at cluster creation>
LOCATION=<cluster's location>
az identity create --name $IDENTITY_NAME --resource-group $RESOURCE_GROUP --location $LOCATION
```
2. Grant the required permissions to the created identity
```
CLIENT_ID=$(az identity show  --name $IDENTITY_NAME --resource-group $RESOURCE_GROUP --query clientId -o tsv)
TENANT_ID=$(az identity show  --name $IDENTITY_NAME --resource-group $RESOURCE_GROUP --query tenantId -o tsv)
SUBSCRIPTION_ID=$(az account show --query id -o tsv)
ROLES=("Reader" "Virtual Machine Contributor" "Network Contributor" "Storage Account Contributor" "Compute Gallery Artifacts Publisher")
for r in "${ROLES[@]}"; do az role assignment create --assignee "$CLIENT_ID" --role "$r" --scope "/subscriptions/$SUBSCRIPTION_ID"; done
```
3. Create an Azure federated identity credential that links the service account in OpenShift Container Platform to the Azure user-assigned managed identity
```
ISSUER=$(oc get authentication.config.openshift.io cluster -o jsonpath='{.spec.serviceAccountIssuer}')
az identity federated-credential create  --name $IDENTITY_NAME-federation  --identity-name $IDENTITY_NAME --issuer $ISSUER --subject system:serviceaccount:openshift-sandboxed-containers-operator:default -g $RESOURCE_GROUP --audiences openshift
```
4. Follow OSC installation instructions. At Subscription creation, you'll need to provide Client ID, Tenant ID, and Subscription ID from above (also when installing from the web console).
Also, it is recommended to use a manual install plan
```
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: sandboxed-containers-operator
  namespace: openshift-sandboxed-containers-operator
spec:
  config:
    env:
    - name: CLIENTID
      value: ${CLIENT_ID}
    - name: TENANTID
      value: ${TENANT_ID}
    - name: SUBSCRIPTIONID
      value: ${SUBSCRIPTION_ID}
  installPlanApproval: Manual
  channel: stable
  name: sandboxed-containers-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  startingCSV: sandboxed-containers-operator.v1.12.0
```
5. Continue following the OSC installation instructions while making sure your created RG is always the one used (the one in use also above), in peer-pods-cm and in configuring the default worker subnet

#### Credentials Cleanup
1. Delete the identity when you delete OSC:
```
az identity delete --name $IDENTITY_NAME --resource-group $RESOURCE_GROUP --location $LOCATION
```
### How it works

**Setup Flow:**
1. User creates an identity within their resource group
2. User assigns the required roles to this identity (based on what CAA/Job need to do)
3. User creates a federated identity credential that points to the cluster's OIDC issuer (this is the identity provider created by ARO/ccoctl with the cluster as the signer). This tells Azure (Entra ID) to trust this issuer (which provides the cluster's signature) for this subject for certain roles and audience.
4. During operator installation via OLM/web console, user provides the following:
   - Azure Client ID (CLIENTID)
   - Azure Tenant ID (TENANTID)
   - Azure Subscription ID (SUBSCRIPTIONID)
5. Setup: On KataConfig creation, the operator consumes the variables and creates `peer-pods-secret` and mounts the service account projected volume
   with federated token configuration:
   ```yaml
   AZURE_CLIENT_ID: <from-env>
   AZURE_TENANT_ID: <from-env>
   AZURE_SUBSCRIPTION_ID: <from-env>
   AZURE_FEDERATED_TOKEN_FILE: /var/run/secrets/openshift/serviceaccount/token
   ```
6. Token fetching: CAA DaemonSet and image jobs consume the federated signed Kubernetes token from the projected service account token file.
7. Exchange: Azure's SDK used by the jobs/CAA code sends this token to Microsoft Entra ID
8. Validation: Entra ID checks if it "trusts" the cluster that issued this token
9. Access: If the trust is verified, Entra ID exchanges the Kubernetes token for an Azure Access Token, which is used to perform whatever is allowed under the defined roles (e.g., create image, create VM, etc.)

**Cleanup Flow:**
1. On KataConfig deletion or `enablePeerPods: false`:
   - Operator identifies STS secret by its label and deletes `peer-pods-secret`

### Identification
- Secret is labeled with `kataconfiguration.openshift.io/sts: azure`

---

## Flow 3: CCO Workflow (Cloud Credential Operator)

**Priority**: Third (fallback, automatic)

**Supported Providers**: AWS, Azure, GCP

### Description
Leverages OpenShift's Cloud Credential Operator to dynamically provision cloud credentials. The operator creates a CredentialsRequest, CCO provisions credentials, and the credentials controller translates them to the peer-pods format.

### User Guide
#### Credentials Setup
1. The peer-pods-secret will be created automatically
#### Credentials Cleanup
1. The peer-pods-secret will be deleted automatically


### How it works

**Setup Flow:**
1. KataConfig created with enablePeerPods: true
2. credentials-controller creates CredentialsRequest for provider
3. CCO creates cco-secret in response to CredentialsRequest
4. credentials-controller triggered by cco-secret creation
5. credentials-controller translates cco-secret to peer-pods-secret
6. peer-pods-secret owned by cco-secret (via OwnerReference)

**Cleanup Flow:**
1. KataConfig deleted or enablePeerPods: false
2. credentials-controller deletes CredentialsRequest
3. CCO deletes cco-secret
4. Garbage Collector deletes owned peer-pods-secret

### Identification
CCO-created secrets are identified by:
- Owner reference: controlled by `cco-secret`
- Kind: `Secret`
- Name: `cco-secret`

---
