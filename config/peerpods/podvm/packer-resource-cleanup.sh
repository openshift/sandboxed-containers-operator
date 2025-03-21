#!/usr/bin/env bash
set -e

CLOUD_PROVIDER=${CLOUD_PROVIDER:-"azure"}

# Define log file path
# This needs to be the PID 1 stdout, which is what is read by `kubectl logs`
# This log will be available in the pod logs
LOG_FILE=${LOG_FILE:-"/proc/1/fd/1"}

# Function to log messages with timestamp
function log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') $1" >>"$LOG_FILE"
}


function azure_cleanup() {
    # Capture Packer created VM names in an array
    log "Deleting Packer VMs"
    mapfile -t vm_names < <(az vm list --resource-group "$AZURE_RESOURCE_GROUP" --query "[?osProfile.adminUsername=='packer' && starts_with(osProfile.computerName, 'pkrvm')].name" --output tsv)

    # Log the captured VM names
    for vm in "${vm_names[@]}"; do
        log "Captured VM: $vm"
    done

    # Check if any VMs were found
    if [ ${#vm_names[@]} -eq 0 ]; then
        log "No VMs found matching the criteria."
    else
        # Loop through the array and delete each VM
        for vm in "${vm_names[@]}"; do
            log "Deleting VM: $vm"
            az vm delete --name "$vm" --resource-group "$AZURE_RESOURCE_GROUP" --yes >>"$LOG_FILE" 2>&1
        done
    fi

    log "Disassociating and Deleting Packer Public IPs..."
    mapfile -t public_ip_names < <(az network public-ip list --resource-group "$AZURE_RESOURCE_GROUP" --query "[?starts_with(name, 'pkrip')].name" --output tsv)

    # Log the captured Public IP names
    for public_ip_name in "${public_ip_names[@]}"; do
        log "Captured Public IP: $public_ip_name"
    done

    for public_ip_name in "${public_ip_names[@]}"; do
        # Get the public IP ID dynamically
        public_ip_id=$(az network public-ip show --name "$public_ip_name" --resource-group "$AZURE_RESOURCE_GROUP" --query "id" --output tsv)

        if [[ -z "$public_ip_id" ]]; then
            log "Skipping $public_ip_name as it has no associated ID."
            continue
        fi

        # Find the NIC associated with this Public IP
        nic_name=$(az network nic list --resource-group "$AZURE_RESOURCE_GROUP" --query "[?ipConfigurations[?publicIPAddress.id=='$public_ip_id']].name" --output tsv)

        if [[ -n "$nic_name" ]]; then
            log "Disassociating Public IP $public_ip_name from NIC $nic_name..."
            az network nic ip-config update --name ipconfig --nic-name "$nic_name" --resource-group "$AZURE_RESOURCE_GROUP" --remove publicIPAddress >>"$LOG_FILE" 2>&1
        fi

        log "Deleting Public IP: $public_ip_name"
        az network public-ip delete --name "$public_ip_name" --resource-group "$AZURE_RESOURCE_GROUP" >>"$LOG_FILE" 2>&1
    done

    log "Deleting Packer Network Interfaces..."
    mapfile -t nics < <(az network nic list --resource-group "$AZURE_RESOURCE_GROUP" --query "[?starts_with(name, 'pkrni')].name" --output tsv)

    # Log the captured NIC names
    for name in "${nics[@]}"; do
        log "Captured NIC: $name"
    done

    for name in "${nics[@]}"; do
        log "Deleting NIC: $name"
        az network nic delete --name "$name" --resource-group "$AZURE_RESOURCE_GROUP" >>"$LOG_FILE" 2>&1
    done

    log "Deleting Packer disks..."
    mapfile -t disks < <(az disk list --resource-group "$AZURE_RESOURCE_GROUP" --query "[?starts_with(name, 'pkrdisk')].name" --output tsv)

    # Log the captured Disk names
    for name in "${disks[@]}"; do
        log "Captured Disk: $name"
    done

    for name in "${disks[@]}"; do
        log "Deleting Disk: $name"
        az disk delete --name "$name" --resource-group "$AZURE_RESOURCE_GROUP" --yes >>"$LOG_FILE" 2>&1
    done

    log "Deleting Packer Virtual Networks..."
    mapfile -t vnets < <(az network vnet list --resource-group "$AZURE_RESOURCE_GROUP" --query "[?starts_with(name, 'pkrvn')].name" --output tsv)
    for vnet in "${vnets[@]}"; do
        log "Deleting VNet: $vnet (this will also remove subnets)"
        az network vnet delete --name "$vnet" --resource-group "$AZURE_RESOURCE_GROUP" >>"$LOG_FILE" 2>&1
    done
}

# Log the start of the script
log "Starting Packer resource cleanup script."

# Execute cleanup if CLOUD_PROVIDER==azure
if [ "$CLOUD_PROVIDER" == "azure" ]; then
    azure_cleanup
else
    log "Cleanup not supported for CLOUD_PROVIDER: $CLOUD_PROVIDER"
fi

log "cleanup hook completed."
