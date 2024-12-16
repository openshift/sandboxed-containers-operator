#!/bin/bash
# Getting RVPS Parameters

function install_packages() {
    echo "***Installing necessary packages for RVPS values extraction ***"
    dnf install -y python3 python3-cryptography kmod
    echo "***Installation Finished ***"
}

# Function to mount the image and extract se.img
function mount_and_extract_image() {
    local img_path=$1

    # Cleanup any previous files and directories
    rm -rf se.img /mnt/myvm
    mkdir /mnt/myvm

    # Load nbd module and mount the image
    modprobe nbd
    if [ $? -ne 0 ]; then
        echo "Error: Failed to load nbd module."
        exit 1
    fi


    #Check the available nbd and use it
    nbd_available=`lsblk | grep nbd | grep 0B | awk '{print $1}' | head -1`

    #Check if 'nbd' is configured and nbd_available has a valid nbd value
    if [[ ! $nbd_available =~ ^nbd ]]; then
  	echo "nbd is not configured . Please configure it and check the output of 'lsblk' to verify if it lists the nbds"
  	exit 1
    fi

    echo $nbd_available "looks available. Starting Mounting the image with it.."
    qemu-nbd -c /dev/$nbd_available $img_path

    if [ $? -ne 0 ]; then
        echo "Error: Failed to connect to nbd device."
        exit 1
    fi



    mount /dev/"$nbd_available"p1 /mnt/myvm
    if [ $? -ne 0 ]; then
         echo "Error: Failed to mount the image. Retrying..."
         sleep 2
         mount /dev/"$nbd_available"p1 /mnt/myvm
         if [ $? -ne 0 ]; then
	       echo "Retrial of mounting is failed. Please check if LPAR is overloaded or try one LPAR reboot"
	       qemu-nbd -d /dev/"$nbd_available"
               exit 1
         else
               echo "Mounting on second attempt passed"
         fi

    fi

    # Extract and process image
    rm -rf $PWD/output-files
    mkdir -p $PWD/output-files
    rm -rf se.img
    cp /mnt/myvm/se.img ./
    mv se.img $PWD/output-files/

    umount /mnt/myvm
    qemu-nbd -d /dev/$nbd_available
}

# Function to generate se-sample and ibmse-policy.rego files
function generate_policy_files() {
    local se_tag=$1
    local se_image_phkh=$2

    if [ -z "$se_image_phkh" ]; then
        echo "There seems to be some issue in HKD.crt. Please use the correct one and run it again."
        exit 1
    fi


    # Create se-sample file
    cat <<EOF > $PWD/output-files/se-sample
{
    "se.attestation_phkh": [
        "$se_image_phkh"
    ],
    "se.tag": [
        "$se_tag"
    ],
    "se.image_phkh": [
        "$se_image_phkh"
    ],
    "se.user_data": [
        "00"
    ],
    "se.version": [
        "256"
    ]
}
EOF

    # Create ibmse-policy.rego file
    cat <<EOF > $PWD/output-files/ibmse-policy.rego
package policy
import rego.v1
default allow = false
converted_version := sprintf("%v", [input["se.version"]])
allow if {
    input["se.attestation_phkh"] == "$se_image_phkh"
    input["se.image_phkh"] == "$se_image_phkh"
    input["se.tag"] == "$se_tag"
    input["se.user_data"] == "00"
    converted_version == "256"
}
EOF

}

# Main function
install_packages

PS3='Please enter your choice: '
options=("Generate the RVPS From Local Image from User pc" "Generate RVPS from Volume"  "Quit")
select opt in "${options[@]}"
do
    case $opt in
        "Generate the RVPS From Local Image from User pc")
            echo "Enter the Qcow2 image with Full path"
            read -r img_path

            mount_and_extract_image $img_path

            $PWD/static-files/pvextract-hdr -o $PWD/output-files/hdr.bin $PWD/output-files/se.img

            # Extract necessary values
            se_tag=$(python3 $PWD/static-files/se_parse_hdr.py $PWD/output-files/hdr.bin $PWD/static-files/HKD.crt | grep se.tag | awk -F ":" '{ print $2 }')
            se_image_phkh=$(python3 $PWD/static-files/se_parse_hdr.py $PWD/output-files/hdr.bin $PWD/static-files/HKD.crt | grep se.image_phkh | awk -F ":" '{ print $2 }')

            echo "se.tag: $se_tag"
            echo "se.image_phkh: $se_image_phkh"

            generate_policy_files $se_tag $se_image_phkh

            provenance=$(cat $PWD/output-files/se-sample | base64 --wrap=0)
            echo "provenance = $provenance"

            # Create se-message file
            cat <<EOF > $PWD/output-files/se-message
{
    "version" : "0.1.0",
    "type": "sample",
    "payload": "$provenance"
}
EOF

            ls -lrt $PWD/output-files/hdr.bin $PWD/output-files/se-message $PWD/output-files/ibmse-policy.rego
            ;;

        "Generate RVPS from Volume")
            echo "Enter the Libvirt Pool Name"
            read -r LIBVIRT_POOL
            echo "Enter the Libvirt URI Name"
            read -r LIBVIRT_URI
            echo "Enter the Libvirt Volume Name"
            read -r LIBVIRT_VOL

            # Download the volume
            echo "Downloading from PODVM Volume..."
            rm -rf $PWD/PODVM-VOL-IMAGE
            mkdir -p $PWD/PODVM-VOL-IMAGE
            virsh -c $LIBVIRT_URI vol-download --vol $LIBVIRT_VOL --pool $LIBVIRT_POOL --file $PWD/PODVM-VOL-IMAGE/podvm_test.qcow2 --sparse
            if [ $? -ne 0 ]; then
                echo "Downloading Failed"
                exit 1
            fi

            img_path=$PWD/PODVM-VOL-IMAGE/podvm_test.qcow2

            mount_and_extract_image $img_path

            $PWD/static-files/pvextract-hdr -o $PWD/output-files/hdr.bin $PWD/output-files/se.img

            # Extract necessary values
            se_tag=$(python3 $PWD/static-files/se_parse_hdr.py $PWD/output-files/hdr.bin $PWD/static-files/HKD.crt | grep se.tag | awk -F ":" '{ print $2 }')
            se_image_phkh=$(python3 $PWD/static-files/se_parse_hdr.py $PWD/output-files/hdr.bin $PWD/static-files/HKD.crt | grep se.image_phkh | awk -F ":" '{ print $2 }')

            echo "se.tag: $se_tag"
            echo "se.image_phkh: $se_image_phkh"

            generate_policy_files $se_tag $se_image_phkh

            provenance=$(cat $PWD/output-files/se-sample | base64 --wrap=0)
            echo "provenance = $provenance"

            # Create se-message file
            cat <<EOF > $PWD/output-files/se-message
{
    "version" : "0.1.0",
    "type": "sample",
    "payload": "$provenance"
}
EOF

            ls -lrt $PWD/output-files/hdr.bin $PWD/output-files/se-message $PWD/output-files/ibmse-policy.rego
            ;;

        "Quit")
            break
            ;;

        *) echo "Invalid option: $REPLY";;
    esac
done

