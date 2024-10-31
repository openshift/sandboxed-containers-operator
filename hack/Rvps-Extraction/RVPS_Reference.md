
# RVPS (Reference Value Provider Service) Usage

The RVPS (Reference Value Provider Service) values are used for remote attestation.

It is responsible for verifying, storing, and providing reference values. RVPS receives and verifies inputs from the software supply chain, stores the measurement values, and generates reference value claims for the Attestation Service.

This operation is performed based on the evidence verified by the Attestation Service (AS).

## RVPS Values

The values are:

1. `image_phkh`
2. `image_tag`
3. `se.version`
4. `se.tag`
5. `se.attestation_phk`

## Script Options

The script will help retrieve the RVPS via the following two options:

1. **Calculate the RVPS values based on the SE PODVM image stored locally** on the user’s machine where the script is being executed. The script will expect the absolute path of the SE PODVM image.

2. **Calculate the RVPS values based on the SE PODVM image uploaded to a libvirt volume**. The script will expect the following inputs:
    - Libvirt Pool Name
    - Libvirt URI Name
    - Libvirt Volume Name

## Output

After successful execution, you will get `se-message` and `ibmse-policy.rego` in a directory called `output-files`. These files will contain the RVPS parameters.

## Prerequisites

The user needs to copy the `Rvps-Extraction` folder locally:

```bash
[root@a3elp36 Rvps-Extraction]# ls -lrt
total 8
drwxr-xr-x. 2 root root   65 Oct 19 16:52 static-files
-rwxr-xr-x. 1 root root 6078 Oct 19 16:52 GetRvps.sh
```

Once copied, the script can be executed as follows:

```bash
./GetRvps.sh
```

### Options
1. Generate the RVPS from a local image on the user’s PC
2. Generate RVPS from a volume
3. Quit

Once the script finishes, the output directory will be created, and the files will be copied to the same path where the script is executed. For example:

```bash
-rw-r--r--. 1 root root 640 Oct 9 13:25 /root/gaurav-rvps-test/COCO-1010/output-files/hdr.bin
-rw-r--r--. 1 root root 446 Oct 9 13:25 /root/gaurav-rvps-test/COCO-1010/output-files/ibmse-policy.rego
-rw-r--r--. 1 root root 561 Oct 9 13:25 /root/gaurav-rvps-test/COCO-1010/output-files/se-message
```

## Static Files

Some static files will also be used to generate the RVPS. These include:

- **`pvextract-hdr`**: This is used to extract the SE header from the PODVM SE image (input). It generates an intermediate file, `hdr.bin`, which will be used for further extraction.
- **`se_parse_hdr.py`**: A Python parser used to generate the actual RVPS values.
- **`HKD.crt`**: This certificate will vary between labs. The user needs to copy the same `HKD.crt` used to generate the uploaded PODVM SE image into this path.
