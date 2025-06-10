#!/usr/bin/env bash
# This script is used to create the bucket and the service role needed for the AMI creation
# it also asks for the credentials and set the secret needed for podvm image (AMI) creation
# which is executed during the sandboxed containers operator installtion process (skip with -s)

set -e

function usage() {
	cat <<EOF
Usage: $(basename $0) [options]
  options:
   -b <bucket name> Set the bucket name (otherwise it will be randomly generated)
   -c               Clean credentials and exit
   -d               Delete the bucket and exit
   -h               Print this help message
   -i               In cluster
   -r <region>      Set the region (otherwise it will be fetched from the cluster)
   -s               Skip credentials request
EOF
}

while getopts ":b:cdhir:s" opt; do
	case ${opt} in
		b ) BUCKET_NAME=$OPTARG;;
		c ) clean_credentials=true;;
		d ) delete_bucket=true;;
		h ) usage && exit 0;;
		i ) in_cluster=true;;
		r ) REGION=$OPTARG;;
		s ) skip_cr=true;;
		\? ) echo "Invalid option: -$OPTARG" >&2 && usage && exit 1;;
	esac
done

extended_credentials() {
	echo "Creating extended credentials for IAM role management, bucket creation and operation credentials"

	ROLE_CREATION_CR_FILE="${TMPDIR}/vmimport_role_creation_cr.yaml"

	cat <<EOF > "${ROLE_CREATION_CR_FILE}"
apiVersion: cloudcredential.openshift.io/v1
kind: CredentialsRequest
metadata:
  name: openshift-sandboxed-containers-aws-image
  namespace: openshift-cloud-credential-operator
spec:
  providerSpec:
    apiVersion: cloudcredential.openshift.io/v1
    kind: AWSProviderSpec
    statementEntries:
    - effect: Allow
      action:
        - iam:CreateRole
        - iam:PutRolePolicy
        - iam:GetRole
        - iam:ListRolePolicies
        - iam:DeleteRole
        - iam:DeleteRolePolicy
      resource: "arn:aws:iam::*:role/vmimport"
    - effect: Allow
      action:
        - s3:CreateBucket
        - s3:DeleteBucket
        - s3:GetBucketLocation
        - s3:ListBucket
        # below are operational credentials
        - s3:GetBucketAcl
      resource: "arn:aws:s3:::${BUCKET_NAME}"
    - effect: Allow
      action:
        - s3:GetOcbject
        - s3:PutObject
        - s3:DeleteObject
      resource: "arn:aws:s3:::${BUCKET_NAME}/*"
    - effect: Allow
      action:
        - ec2:CancelConversionTask
        - ec2:CancelExportTask
        - ec2:CreateImage
        - ec2:CreateInstanceExportTask
        - ec2:CreateTags
        - ec2:DescribeConversionTasks
        - ec2:DescribeExportTasks
        - ec2:DescribeExportImageTasks
        - ec2:DescribeImages
        - ec2:DescribeInstanceStatus
        - ec2:DescribeInstances
        - ec2:DescribeSnapshots
        - ec2:DescribeTags
        - ec2:DescribeRegions
        - ec2:ExportImage
        - ec2:ImportInstance
        - ec2:ImportVolume
        - ec2:StartInstances
        - ec2:StopInstances
        - ec2:TerminateInstances
        - ec2:ImportImage
        - ec2:ImportSnapshot
        - ec2:DescribeImportImageTasks
        - ec2:DescribeImportSnapshotTasks
        - ec2:CancelImportTask
        - ec2:RegisterImage
        - s3:ListAllMyBuckets
      resource: "*"

  secretRef:
    name: ${SECRET_NAME}
    namespace: openshift-sandboxed-containers-operator
EOF

	echo "Applying CredentialsRequest for role creation"
	oc apply -f "${ROLE_CREATION_CR_FILE}"

	echo "Waiting for role creation secret to be created"
	while ! oc get secret ${SECRET_NAME} -n openshift-sandboxed-containers-operator >/dev/null 2>&1; do
		echo "Waiting for secret ${SECRET_NAME} with extended credentials..."
		sleep 5
	done

	echo "Extracting and setting up AWS credentials for role creation, bucket creation and operation"
	# Extract credentials from the secret & Credentials health check
	for i in {1..5}; do
		export AWS_ACCESS_KEY_ID=$(oc get secret ${SECRET_NAME} -n openshift-sandboxed-containers-operator -o jsonpath='{.data.aws_access_key_id}' | base64 -d)
		export AWS_SECRET_ACCESS_KEY=$(oc get secret ${SECRET_NAME} -n openshift-sandboxed-containers-operator -o jsonpath='{.data.aws_secret_access_key}' | base64 -d)
		if aws sts get-caller-identity &>/dev/null; then
			break
		fi
		if [[ $i -eq 5 ]]; then
			echo "Failed to get valid credentials"
			exit 1
		fi
		sleep 5
	done

	echo "Extended credentials created, these should be used for bucket creation, vmimport role creation and operation"
}

prepare() {
	TMPDIR=$(mktemp -d)
	TRUST_POLICY_JSON_FILE="${TMPDIR}/trust-policy.json"
	ROLE_POLICY_JSON_FILE="${TMPDIR}/role-policy.json"
	CREDENTIALS_REQUEST_YAML_FILE="${TMPDIR}/vmimport_credentials_request.yaml"
	CREDENTIALS_REQUEST_JSON_FILE="${TMPDIR}/update_credentials_request.json"
	SECRET_NAME="peer-pods-image-creation-secret"
	echo "Temporary Workdir: ${TMPDIR}"
}

init() {
	command -v oc &> /dev/null ||  { echo "oc command was not found" 1>&2 ; exit 1; }
	oc cluster-info &> /dev/null ||  { echo "cluster is not configured" 1>&2 ; exit 1; }
	[[ -x "$(command -v aws)" ]] || { echo "aws is not installed" 1>&2 ; exit 1; }
	aws sts get-caller-identity &>/dev/null || { echo "aws cli missing credentials"; exit 1; }
	[[ $REGION ]] || REGION=$(oc get infrastructure -n cluster -o=jsonpath='{.items[0].status.platformStatus.aws.region}') || { echo "Region couln't be fetched, add as argument" 1>&2 ; exit 1; }
	[[ ! $BUCKET_NAME ]] && uid=$(kubectl get infrastructure -n cluster -o jsonpath='{.items[*].metadata.uid}') && BUCKET_NAME=osc-${uid:0:6}-bucket && \
	echo "Bucket name not provided, using ${BUCKET_NAME}, make sure to set it in the aws-podvm-image-cm"

	echo "Bucket Name: ${BUCKET_NAME}"
	echo "Region: ${REGION}"
}

delete_bucket() {
	echo "Delete bucket content"
	aws s3 rm s3://${BUCKET_NAME} --recursive --region ${REGION}

	echo "Delete s3 bucket named ${BUCKET_NAME} at ${REGION}"
	aws s3api delete-bucket --bucket ${BUCKET_NAME} --region ${REGION}
}

create_bucket() {
	echo "Create s3 Bucket named ${BUCKET_NAME} at ${REGION}"
	if [[ ${REGION} == us-east-1 ]]; then
		aws s3api create-bucket  --bucket ${BUCKET_NAME} --region ${REGION}
	else
		aws s3api create-bucket  --bucket ${BUCKET_NAME} --region ${REGION} --create-bucket-configuration LocationConstraint=${REGION}
	fi
}

set_service_role() {
	echo "Create the service role"
	cat <<EOF > "${TRUST_POLICY_JSON_FILE}"
{
	"Version":"2012-10-17",
	"Statement":[
		{
			"Effect":"Allow",
			"Principal":{ "Service":"vmie.amazonaws.com" },
			"Action": "sts:AssumeRole",
			"Condition":{"StringEquals":{"sts:Externalid":"vmimport"}}
		}
	]
}
EOF

	if ! aws iam get-role --role-name vmimport --region ${REGION} &>/dev/null; then
		aws iam create-role --role-name vmimport --assume-role-policy-document "file://${TRUST_POLICY_JSON_FILE}" --region ${REGION}
	else
		echo "Role vmimport already exists, continue with policy attachment"
	fi

	echo "Attach policy"
	cat <<EOF > "${ROLE_POLICY_JSON_FILE}"
{
	"Version":"2012-10-17",
	"Statement":[
		{
			"Effect":"Allow",
			"Action":["s3:GetBucketLocation","s3:GetObject","s3:ListBucket"],
			"Resource":["arn:aws:s3:::${BUCKET_NAME}","arn:aws:s3:::${BUCKET_NAME}/*"]
		},
		{
			"Effect":"Allow",
			"Action":["ec2:ModifySnapshotAttribute","ec2:CopySnapshot","ec2:RegisterImage","ec2:Describe*"],
			"Resource":"*"
		}
	]
}
EOF

	aws iam put-role-policy --role-name vmimport --policy-name vmimport --policy-document "file://${ROLE_POLICY_JSON_FILE}" --region ${REGION}
}

clean_credentials() {
	echo "Clean credentials"
	oc delete credentialsrequest openshift-sandboxed-containers-aws-image -n openshift-cloud-credential-operator
}

get_operation_credentials() {
	[[ $skip_cr ]] && return
	echo "Ask for addtional credentials"
	oc get ns openshift-sandboxed-containers-operator >/dev/null 2>&1 || (echo "OSC namespace is missing, re-run after installting the operator" && exit 1)

	cat <<EOF > "${CREDENTIALS_REQUEST_YAML_FILE}"
apiVersion: cloudcredential.openshift.io/v1
kind: CredentialsRequest
metadata:
  name: openshift-sandboxed-containers-aws-image
  namespace: openshift-cloud-credential-operator
spec:
  providerSpec:
    apiVersion: cloudcredential.openshift.io/v1
    kind: AWSProviderSpec
    statementEntries:
    - effect: Allow
      action:
        - s3:GetBucketLocation
        - s3:GetBucketAcl
      resource: "arn:aws:s3:::${BUCKET_NAME}"
    - effect: Allow
      action:
        - s3:GetOcbject
        - s3:PutObject
        - s3:DeleteObject
      resource: "arn:aws:s3:::${BUCKET_NAME}/*"
    - effect: Allow
      action:
        - ec2:CancelConversionTask
        - ec2:CancelExportTask
        - ec2:CreateImage
        - ec2:CreateInstanceExportTask
        - ec2:CreateTags
        - ec2:DescribeConversionTasks
        - ec2:DescribeExportTasks
        - ec2:DescribeExportImageTasks
        - ec2:DescribeImages
        - ec2:DescribeInstanceStatus
        - ec2:DescribeInstances
        - ec2:DescribeSnapshots
        - ec2:DescribeTags
        - ec2:DescribeRegions
        - ec2:ExportImage
        - ec2:ImportInstance
        - ec2:ImportVolume
        - ec2:StartInstances
        - ec2:StopInstances
        - ec2:TerminateInstances
        - ec2:ImportImage
        - ec2:ImportSnapshot
        - ec2:DescribeImportImageTasks
        - ec2:DescribeImportSnapshotTasks
        - ec2:CancelImportTask
        - ec2:RegisterImage
        - s3:ListAllMyBuckets
      resource: "*"
  secretRef:
    name: ${SECRET_NAME}
    namespace: openshift-sandboxed-containers-operator
EOF
	oc apply -f ${CREDENTIALS_REQUEST_YAML_FILE}
	while ! oc get secret ${SECRET_NAME} -n openshift-sandboxed-containers-operator; do echo "Waiting for secret."; sleep 1; done
	# Convert key names to uppercase as expected
	oc get secret ${SECRET_NAME} -n openshift-sandboxed-containers-operator -o yaml | sed -E 's/aws_([a-z]|_)*:/\U&/g' | oc replace -f -
}

init

prepare

[[ $in_cluster ]] && extended_credentials

[[ $clean_credentials ]] && clean_credentials; [[ $delete_bucket ]] && delete_bucket; [[ $clean_credentials || $delete_bucket ]] && exit 0

create_bucket

set_service_role

[[ $in_cluster ]] || get_operation_credentials
