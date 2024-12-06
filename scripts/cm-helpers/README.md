## OSC ConfigMap Configurator

./pp-cm-helper.sh is an interactive Bash script designed to assist in populating the peer-pods-cm
ConfigMap, which is essential for configuring Peer Pods or CoCo.
The script intelligently suggests default values based on metadata collected from the cluster's
cloud provider, recommended best practices, and user-provided input.

### Supported Cloud Providers
* AWS
* Azure

### Prerequisites
* jq, kubectl or oc installed
* Preconfigured OCP cluster with OSC Operator installed

### Usage:
./pp-cm-helper.sh [options]
  options:
   -c <sev/tdx>    Use CoCo defaults for the specified trusted platform type
   -h              Print this help message
   -v <KEY=value>  Set a known or custom variable explicitly
   -y              Automatically answer yes for all questions

  * Defaults are fetched according to the following order:
    1. Explicitly set CLI custom vars
    2. Explicitly defined enviroment vars
    3. Fixed/Fetched/Existing values
