# Hegel Instance Metadata Standard

## Motivation

Hegel is an instance metadata service. It serves instance metadata over an API (often HTTP) based on the requester IP address (source address). The kind of information it serves include host name, IP addresses, and instance IDs. The data served can be used by consumers to configure the instance. For example, cloud-init parses instance metadata into a format that can be used by user-data scripts when executed by cloud-init.

Currently, Hegel exposes [Equinix Metal instance metadata](https://metal.equinix.com/developers/docs/server-metadata/metadata/). This metadata standard is useful, but is constrained in the extent it can be modified or configured because of its compliance to the Equinix metal instance metadata standard. We want to develop a new Hegel metadata API that compliments Tinkerbell more generally and can be extended as necessary, while continuing to support Equinix’s metadata standard. 

The extensibility of an independent Hegel metadata API will enable introduction of data, such as disk information, that creates increased flexibility in template generation as they become machine agnostic.

## API Proposal

The following table details the endpoints of instance metadata. Some of the endpoints include placeholders for data that is unique to your instance. 

|Endpoint|Description|Version|
|--------|-----------|-------|
|/metadata/v0/hostname|The hostname of the instance.|	v0|
|/metadata/v0/`<interface>`/ipv4/`<index>`/ip	|The IPv4 interface for the interface.	|v0|
|/metadata/v0/`<interface>`/ipv4/`<index>`/netmask	|The subnet mask for the IP configuration.	|v0|
|/metadata/v0/`<interface>`/ipv6/`<index>`/ip	|The IPv6 address for the interface.	|v0
|/metadata/v0/`<interface>`/ipv6/`<index>`/netmask	|The subnet mask for the IP address.	|v0
|/metadata/v0/gateway	|The instance gateway IP address.	|v0
|/metadata/v0/user-data	|User-data of the instance.	|v0
|/metadata/v0/vendor-data	|Vendor-data for the instance.	|v0
|/metadata/v0/disks/`<index>`	|Information about a single disk such as device name. For example, /dev/sda	|v0
|/metadata/v0/ssh-public-keys/`<number>`	|SSH keys for the instance.|	v0

## Default API, Toggleability and Configuration

To maintain backwards compatibility the existing Equinix focused API will be exposed by default. This includes the HTTP and gRPC endpoints. We will look to change the default API in the future.

APIs will be togglable based through CLI or environment variable configuration that compliment the existing implementation. Only the default APIs or the Hegel metadata API will be served at any one time. This aids in avoiding confusion.

The API being served will be exposed via the `/versions` endpoint.

## Versioning

Endpoints are versioned using a single number. When new endpoints are released the version number will be incremented. In most circumstances, we should strive to maintain existing endpoints in subsequent versions. The version number is not semantic.

## Implementation

The code currently proclaims EC2 instance metadata compliance. [Given this is inaccurate](https://github.com/tinkerbell/hegel/issues/61#issuecomment-1120483426) we will make the necessary corrections to names. 

We will look to introduce a new [set of filters](https://github.com/tinkerbell/hegel/blob/main/http/handlers.go#L28) for the Hegel metadata and [refactor existing HTTP handlers](https://github.com/tinkerbell/hegel/blob/main/http/handlers.go#L163) to be generalized. The new handler will be used as the business logic for serving both existing APIs and the new API. 


## Questions, comments, or concerns

* How should IP endpoints be configured? Is <interface> a MAC or an interface name?
* How do we deal with IPv6 vs IPv4?
* Additional endpoints?
