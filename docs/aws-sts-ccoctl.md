# AWS STS Workflow Using ccoctl

This guide describes how to set up AWS STS (IRSA) credentials for the
sandboxed-containers-operator using `ccoctl`. This is an alternative to the automatic
[ROLEARN-based STS workflow](credentials-handling.md) and is suited for restricted environments
where the operator cannot create IAM roles itself.

## How It Works

1. You run `ccoctl` locally to create an IAM role and generate a `cco-secret` manifest
2. You apply the generated `cco-secret` to the cluster
3. The operator's credentials controller detects `cco-secret` and automatically converts it into
   `peer-pods-secret` with the correct environment variable format (`AWS_ROLE_ARN`,
   `AWS_WEB_IDENTITY_TOKEN_FILE`)
4. You install the operator and create a KataConfig — `peer-pods-secret` already exists, so no
   `ROLEARN` env var is needed in the Subscription

## Prerequisites

- AWS CLI installed and configured with IAM permissions to create roles and policies
- `oc` CLI authenticated to your OpenShift cluster
- OpenShift cluster with OIDC issuer configured (standard for AWS clusters)

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
mkdir -p credrequests

OPERATOR_POD=$(oc get pods -n openshift-sandboxed-containers-operator \
  -l control-plane=controller-manager \
  -o jsonpath='{.items[0].metadata.name}')

oc cp -n openshift-sandboxed-containers-operator \
  "${OPERATOR_POD}:/config/peerpods/credentials-requests/ccoctl/credentials_request_aws_sts.yaml" \
  credrequests/credentials_request_aws_sts.yaml
```

### 3. Run ccoctl to create the IAM role and generate the secret manifest

```bash
./ccoctl aws create-iam-roles \
  --name=sandboxed-containers \
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

The credentials controller will detect `cco-secret`, parse the AWS config file format, and
automatically create `peer-pods-secret` with:

```
AWS_ROLE_ARN=<role ARN created by ccoctl>
AWS_WEB_IDENTITY_TOKEN_FILE=/var/run/secrets/openshift/serviceaccount/token
```

### 5. Verify peer-pods-secret was created

```bash
oc get secret peer-pods-secret -n openshift-sandboxed-containers-operator -o yaml
```

The secret should contain `AWS_ROLE_ARN` and `AWS_WEB_IDENTITY_TOKEN_FILE` keys and carry the
label `kataconfiguration.openshift.io/credentials-request-based: "true"`.

### 6. Continue with OSC installation

Install the operator and create a KataConfig with `enablePeerPods: true`. Because `peer-pods-secret`
already exists, no `ROLEARN` env var is required in the Subscription.

## Reducing Permissions After Image Creation

The CredentialsRequest bundled in the operator image (`credentials_request_aws_sts.yaml`) includes
extended permissions required for the podvm image creation job (S3 bucket management, VMImport IAM
role). Once image creation has completed successfully these permissions are no longer needed and
can be removed to follow the principle of least privilege.

To reduce permissions, create a trimmed CredentialsRequest that contains only the EC2 permissions
required by the cloud-api-adaptor at runtime, then re-run `ccoctl` to update the IAM role policy
in place. The role ARN does not change, so the existing `cco-secret` and `peer-pods-secret` remain
valid — no re-apply is needed.

```bash
# Create a reduced CredentialsRequest (EC2 only, no S3/IAM)
# Note: This will override credentials_request_aws_sts.yaml due to alphabetical ordering
cat > credrequests/credentials_request_aws_sts_minimal.yaml <<'EOF'
apiVersion: cloudcredential.openshift.io/v1
kind: CredentialsRequest
metadata:
  name: openshift-sandboxed-containers-aws
  namespace: openshift-cloud-credential-operator
spec:
  secretRef:
    name: cco-secret
    namespace: openshift-sandboxed-containers-operator
  serviceAccountNames:
    - default
  providerSpec:
    apiVersion: cloudcredential.openshift.io/v1
    kind: AWSProviderSpec
    statementEntries:
      - effect: Allow
        action:
          - ec2:*
        resource: "*"
EOF

# Re-run ccoctl — updates the IAM role policy without recreating the role or secret
./ccoctl aws create-iam-roles \
  --name=sandboxed-containers \
  --region="${AWS_REGION}" \
  --credentials-requests-dir=./credrequests \
  --output-dir=./ccoctl-output \
  --identity-provider-arn="${IDENTITY_PROVIDER_ARN}"
```

> **Note:** If you need to create a new podvm image in the future, re-run `ccoctl` with the full
> `credentials_request_aws_sts.yaml` (fetched from the operator image) to restore the extended
> permissions, then reduce them again once image creation completes.

## Cleanup

Delete `cco-secret` first (this cascades to `peer-pods-secret` via owner reference), then remove
the IAM role created by `ccoctl`:

```bash
oc delete secret cco-secret -n openshift-sandboxed-containers-operator

./ccoctl aws delete \
  --name=sandboxed-containers \
  --region="${AWS_REGION}"
```
