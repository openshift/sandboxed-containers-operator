# Peer-Pods Credentials Handling

This document describes credential configuration options for the OpenShift Sandboxed Containers Operator peer-pods functionality.

## Overview

The operator requires cloud credentials to provision and manage peer-pods workloads. Three credential flows are supported, with availability depending on cloud provider and cluster operation mode:

1. **STS workflow** (Tokenized authentication): AWS, Azure
2. **CCO workflow** (Cloud Credential Operator): AWS, Azure, GCP
3. **User-created secret**: All providers

The credential flow selection happens automatically during KataConfig creation when `enablePeerPods: true`, based on the existence and type of user-provided credentials.

---

## AWS Credentials

### Option 1: Tokenized Authentication (IRSA)

#### Description
Uses AWS IAM Roles for Service Accounts (IRSA) for authentication without long-lived secrets. This is the recommended option for AWS credentials. When the cluster is configured for tokenized authentication and appropriate credentials were provided during OLM installation (and the user has not provided credentials in peer-pods-secret), the operator reads environment variables set during OLM installation and creates a secret containing the provided variables and pointing to the projected volume containing the service account token.

Reference: [OpenShift Enhancement Proposal #1800](https://github.com/openshift/enhancements/pull/1800)

#### Prerequisites
- [AWS CLI](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) installed locally
- AWS credentials with IAM permissions to create roles and policies (one-time setup only).

#### Credentials Setup

**Note**: This setup uses a single IAM role with two managed policies - a base policy for runtime CAA operations and an extended policy for image creation operations. The image creation policy can be detached after image creation to follow the principle of least privilege.

1. Get AWS account ID and OIDC Provider
```bash
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query "Account" --output text)
OIDC_PROVIDER=$(oc get authentication cluster -ojson | jq -r .spec.serviceAccountIssuer | sed -e "s/^https:\/\///")
```

2. Create the IAM role with trust policy:
```bash
# Set variables
export ROLE_NAME=openshift-sandboxed-containers-role

# Create trust policy document
cat > trust-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::${AWS_ACCOUNT_ID}:oidc-provider/${OIDC_PROVIDER}"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "${OIDC_PROVIDER}:sub": "system:serviceaccount:openshift-sandboxed-containers-operator:default",
          "${OIDC_PROVIDER}:aud": "openshift"
        }
      }
    }
  ]
}
EOF

# Create the IAM role
aws iam create-role \
  --role-name $ROLE_NAME \
  --assume-role-policy-document file://trust-policy.json
```

3. Create and attach the base policy (required for runtime CAA operations):
```bash
# TODO: Use a reduced custom policy with only required EC2 permissions instead of AmazonEC2FullAccess
aws iam attach-role-policy \
  --role-name ${ROLE_NAME} \
  --policy-arn arn:aws:iam::aws:policy/AmazonEC2FullAccess
```

4. Create and attach the extended policy (required only for image creation):
```bash
cat > extended-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "VMImportRoleManagement",
      "Effect": "Allow",
      "Action": [
        "iam:CreateRole",
        "iam:PutRolePolicy",
        "iam:GetRole",
        "iam:ListRolePolicies",
        "iam:DeleteRole",
        "iam:DeleteRolePolicy"
      ],
      "Resource": "arn:aws:iam::${AWS_ACCOUNT_ID}:role/vmimport"
    },
    {
      "Sid": "S3BucketManagement",
      "Effect": "Allow",
      "Action": [
        "s3:CreateBucket",
        "s3:DeleteBucket",
        "s3:GetBucketLocation",
        "s3:ListBucket",
        "s3:GetBucketAcl"
      ],
      "Resource": "arn:aws:s3:::podvm-*"
    },
    {
      "Sid": "S3ObjectManagement",
      "Effect": "Allow",
      "Action": [
        "s3:PutObject",
        "s3:GetObject",
        "s3:DeleteObject"
      ],
      "Resource": "arn:aws:s3:::podvm-*/*"
    },
    {
      "Sid": "S3ListAllBuckets",
      "Effect": "Allow",
      "Action": "s3:ListAllMyBuckets",
      "Resource": "*"
    }
  ]
}
EOF

aws iam create-policy \
  --policy-name OSC-ImageCreation-Policy \
  --policy-document file://extended-policy.json

# Attach for image creation (temporary)
aws iam attach-role-policy \
  --role-name $ROLE_NAME \
  --policy-arn arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OSC-ImageCreation-Policy
```

5. Follow OSC installation instructions. At Subscription creation, provide the IAM Role ARN:
```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: sandboxed-containers-operator
  namespace: openshift-sandboxed-containers-operator
spec:
  config:
    env:
    - name: ROLEARN
      value: arn:aws:iam::${AWS_ACCOUNT_ID}:role/${ROLE_NAME}
  installPlanApproval: Manual
  channel: stable
  name: sandboxed-containers-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  startingCSV: sandboxed-containers-operator.v1.12.1
```

6. Continue following the OSC installation instructions for AWS peer-pods configuration

**Note**: After image creation completes, it's highly recommended to detach the extended policy for security (least privilege). The IAM role ARN remains the same when attaching/detaching policies, so no operator or configuration changes are needed. If you need to recreate images in the future, simply re-attach the extended policy temporarily, then detach it again after completion.

```bash
# Detach extended policy after image creation
aws iam detach-role-policy \
  --role-name $ROLE_NAME \
  --policy-arn arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OSC-ImageCreation-Policy

# Verify only base policy remains
aws iam list-attached-role-policies --role-name $ROLE_NAME
```

#### Credentials Cleanup
1. Detach all policies and delete the IAM role when you delete OSC:
```bash
# Detach policies from IRSA role
aws iam detach-role-policy \
  --role-name $ROLE_NAME \
  --policy-arn arn:aws:iam::aws:policy/AmazonEC2FullAccess

# If still attached
aws iam detach-role-policy \
  --role-name $ROLE_NAME \
  --policy-arn arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OSC-ImageCreation-Policy

aws iam delete-role --role-name $ROLE_NAME

# Delete the custom policy
aws iam delete-policy --policy-arn arn:aws:iam::${AWS_ACCOUNT_ID}:policy/OSC-ImageCreation-Policy
```

#### How it works

**Setup Flow:**
1. User configures the IAM role's trust policy to trust the cluster's OIDC issuer for the `openshift-sandboxed-containers-operator:default` service account
2. User creates role policies with EC2 permissions required by CAA and S3 and IAM permissions required for the image creation Job and attaches them to the IAM role
3. During operator installation via OLM/web console, user provides the created AWS IAM Role ARN (ROLEARN)
4. Setup: On KataConfig creation, the operator consumes the variable and creates `peer-pods-secret` and mounts the service account projected volume
   with IRSA-based authentication configuration:
   ```yaml
   AWS_ROLE_ARN: <from-env>
   AWS_WEB_IDENTITY_TOKEN_FILE: /var/run/secrets/openshift/serviceaccount/token
   ```
5. Token fetching: CAA DaemonSet and image jobs consume the signed Kubernetes token from the projected service account token file.
6. Exchange: AWS SDK used by the jobs/CAA code sends this token to AWS STS (Security Token Service)
7. Validation: AWS STS validates the token against the configured OIDC provider and checks if the issuer is trusted
8. Access: If the trust is verified, AWS STS exchanges the Kubernetes token for temporary AWS credentials, which are used to perform EC2 operations allowed by the IAM role (e.g., create instances, manage volumes, etc.)
9. After image creation use can deatch the extended role policy with the image creation permissions

**Cleanup Flow:**
1. On KataConfig deletion or `enablePeerPods: false`:
   - Operator identifies STS secret by its label and deletes `peer-pods-secret`

---

### Option 2: Cloud Credential Operator (CCO)

**Priority**: Third (fallback, automatic)

#### Description
Leverages OpenShift's Cloud Credential Operator to dynamically provision cloud credentials. The operator creates a CredentialsRequest, CCO provisions credentials, and the credentials controller translates them to the peer-pods format. This is the default option when the user does not provide credentials.

#### Credentials Setup
1. The peer-pods-secret will be created automatically

#### Credentials Cleanup
1. The peer-pods-secret will be deleted automatically

#### How it works

**Setup Flow:**

1. KataConfig created with `enablePeerPods: true`
2. **Image creation credentials:**
    1. Image creation job executes ami-helper.sh script
    2. ami-helper.sh creates CredentialsRequest requesting image creation permissions
    3. CCO creates peer-pods-image-creation-secret in response to CredentialsRequest
    4. ami-helper.sh uses the created credentials to create vmimport role and S3 bucket
    5. ami-helper.sh updates the CredentialsRequest, switching the requested permissions from infrastructure setup permissions to image creation permissions
    6. CCO updates peer-pods-image-creation-secret in response to the updated CredentialsRequest
    7. Image creation job uses the updated credentials for image creation
    8. Image creation job cleans up unused resources

3. **Cloud API adaptor credentials:**
    1. credentials-controller creates CredentialsRequest for provider
    2. CCO creates cco-secret in response to CredentialsRequest
    3. credentials-controller is triggered by cco-secret creation
    4. credentials-controller translates cco-secret to peer-pods-secret
    5. peer-pods-secret is owned by cco-secret (via OwnerReference)

**Cleanup Flow:**
1. KataConfig deleted or enablePeerPods: false
2. credentials-controller deletes CredentialsRequest
3. CCO deletes cco-secret
4. Garbage Collector deletes owned peer-pods-secret

#### Identification
CCO-created secrets are identified by:
- Label `kataconfiguration.openshift.io/credentials-request-based: true`
- OwnerReference to the cco-secret

---

### Option 3: Manual Credentials

#### Description
Users manually create the `peer-pods-secret` in the `openshift-sandboxed-containers-operator` namespace with AWS credentials.

#### Credentials Setup
1. At configuration time (before deploying KataConfig), create `peer-pods-secret` with AWS credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: peer-pods-secret
  namespace: openshift-sandboxed-containers-operator
type: Opaque
data:
  AWS_ACCESS_KEY_ID: <base64-encoded-access-key-id>
  AWS_SECRET_ACCESS_KEY: <base64-encoded-secret-access-key>
```

**AWS-specific requirements**: When using manual AWS credentials for image creation, you must pre-create the S3 bucket and vmimport IAM role before deploying KataConfig. You can use the `ami-helper.sh` script to automate this setup:

```bash
# Set your bucket name and region
export BUCKET_NAME=podvm-image-bucket-name-example
export REGION=us-east-1

# Create bucket and vmimport role (requires AWS credentials with appropriate permissions)
./config/peerpods/podvm/ami-helper.sh -s -b ${BUCKET_NAME} -r ${REGION}

# Set the bucket name in the peer-pods ConfigMap
oc patch configmap peer-pods-cm -n openshift-sandboxed-containers-operator \
  --type merge -p "{\"data\":{\"BUCKET_NAME\":\"${BUCKET_NAME}\"}}"
```

The script will:
- Create an S3 bucket with the specified name
- Set up the vmimport IAM service role for EC2 VM Import
- Grant the vmimport role access to the S3 bucket

Alternatively, you can create these resources manually. The vmimport role trust policy and permissions must allow EC2 VM Import to access your S3 bucket.

#### Credentials Cleanup
1. Delete the S3 bucket and vmimport role (can be done after image creation is completed):
```bash
./config/peerpods/podvm/ami-helper.sh -s -d -b ${BUCKET_NAME} -r ${REGION}
```

2. Delete the secret:
```bash
oc delete secret/peer-pods-secret -n openshift-sandboxed-containers-operator
```

#### How it works
1. User creates `peer-pods-secret` with required cloud credentials
2. Operator detects the existing secret and skips automated credential setup
3. Secret persists until manually deleted by the user

---

## Azure Credentials

### Option 1: Tokenized Authentication (Workload Identity)

#### Description
Uses Azure Workload Identity for authentication without long-lived secrets. This is the recommended option for Azure credentials. When the cluster is configured for tokenized authentication and appropriate credentials were provided during OLM installation (and the user has not provided credentials in peer-pods-secret), the operator reads environment variables set during OLM installation and creates a secret containing the provided variables and pointing to the projected volume containing the service account token.

Reference: [OpenShift Enhancement Proposal #1800](https://github.com/openshift/enhancements/pull/1800)

#### Prerequisites
- [Azure CLI](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli) installed locally
- Azure credentials with permissions to create identities and assign roles (one-time setup only)

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
  startingCSV: sandboxed-containers-operator.v1.12.1
```
5. Continue following the OSC installation instructions while making sure your created RG is always the one used (the one in use also above), in peer-pods-cm and in configuring the default worker subnet

#### Credentials Cleanup
1. Delete the identity when you delete OSC:
```
az identity delete --name $IDENTITY_NAME --resource-group $RESOURCE_GROUP --location $LOCATION
```

#### How it works

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



---

### Option 2: Cloud Credential Operator (CCO)

#### Description
Leverages OpenShift's Cloud Credential Operator to dynamically provision cloud credentials. The operator creates a CredentialsRequest, CCO provisions credentials, and the credentials controller translates them to the peer-pods format. This is the default option when the user does not provide credentials.

#### Credentials Setup
1. The peer-pods-secret will be created automatically

#### Credentials Cleanup
1. The peer-pods-secret will be deleted automatically

#### How it works

**Setup Flow:**
1. KataConfig created with `enablePeerPods: true`
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

#### Identification
CCO-created secrets are identified by:
- Label `kataconfiguration.openshift.io/credentials-request-based: true`
- OwnerReference to the cco-secret

---

### Option 3: Manual Credentials

#### Description
Users manually create the `peer-pods-secret` in the `openshift-sandboxed-containers-operator` namespace with Azure credentials.

#### Credentials Setup
1. At configuration time (before deploying KataConfig), create `peer-pods-secret` with Azure credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: peer-pods-secret
  namespace: openshift-sandboxed-containers-operator
type: Opaque
data:
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

#### How it works
1. User creates `peer-pods-secret` with required cloud credentials
2. Operator detects the existing secret and skips automated credential setup
3. Secret persists until manually deleted by the user
