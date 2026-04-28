#!/bin/bash

# virtiofsd must run as qemu: running as root fails with "can't apply the child
# capabilities" because rootless containers lack the caps virtiofsd tries to set.
mkdir -p /var/lib/libvirt/virtiofsd
chown qemu:qemu /var/lib/libvirt/virtiofsd /var/lib/cluster-images

runuser -u qemu -- /usr/libexec/virtiofsd \
    --socket-path=/var/lib/libvirt/virtiofsd/virtiofsd.sock \
    --shared-dir=/var/lib/cluster-images \
    --sandbox=none \
    --cache=auto &

/usr/sbin/virtlogd &
/usr/bin/virtstoraged &
/usr/sbin/virtnetworkd -d
/usr/sbin/virtqemud -v -t 0
