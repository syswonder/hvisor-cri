# hvcisor-cri
A CRI-compliant runtime for managing hvisor virtual machines as containers in Kubernetes

### Setup Steps
#### Kernel Compilation
Compile the kernel normally, and also compile the modules.
Then install the kernel modules to /lib/modules/$(uname -r).
make modules_install INSTALL_MOD_PATH=/path/to/rootfs

#### disk_img:
Follow https://hvisor.syswonder.org/chap03/BootRootLinux.html normally, but after chroot, besides installing the packages mentioned in the tutorial, `you also need to install systemd`:
```
apt-get install -y systemd systemd-sysv
ln -sf /lib/systemd/systemd /sbin/init
```
After startup, it may get stuck at [  *** ] A start job is running for /dev/ttyAMA0 (1min 12s / 1min 30s). You need:
```
mount -t ext4 rootfs1.img rootfs/
sudo chroot rootfs/
cp lib/systemd/system/serial-getty\@.service lib/systemd/system/serial-getty\@ttyAMA0.service
systemctl enable serial-getty\@ttyAMA0.service
```
Then you also need to modify: /lib/systemd/system/serial-getty@ttyAMA0.service, comment out the two lines marked below
```
[Unit]
Description=Serial Getty on %I
Documentation=man:agetty(8) man:systemd-getty-generator(8)
Documentation=http://0pointer.de/blog/projects/serial-console.html
#BindsTo=dev-%i.device		###this line needs to be commented
#After=dev-%i.device systemd-user-sessions.service plymouth-quit-wait.service getty-pre.target ###this line needs to be commented
After=rc-local.service
```
Then execute:
```
passwd -d root
exit
```
Now you can log in as root

#### Network Configuration
Configure vm according to https://hvisor.syswonder.org/chap04/subchap03/VirtIO/NetDevice.html, but change the final dhcp to:
```
ip addr add 192.168.100.2/24 dev eth0
ip route add default via 192.168.100.1
```
Create a corresponding tap0 device on the host:
```
sudo ip tuntap add dev tap0 mode tap user $(whoami)
sudo ip link set tap0 up
sudo ip addr add 192.168.100.1/24 dev tap0
```
Also add qemu parameters in `hvisor/platform/aarch64/qemu-gicv3/platform.mk`:
```
QEMU_ARGS += -netdev tap,id=net0,ifname=tap0,script=no,downscript=no 
QEMU_ARGS += -device virtio-net-pci,netdev=net0 
```

This enables networking between the host and vm. If the vm needs to connect to the internet, enable port forwarding on the host:
```
echo 1 | sudo tee /proc/sys/net/ipv4/ip_forward
iptables -t nat -A POSTROUTING -s 192.168.100.0/24 -o ens33 -j MASQUERADE
iptables -A FORWARD -i tap0 -j ACCEPT
iptables -A FORWARD -o tap0 -j ACCEPT
```
Query the real network interface with `ip route show default`

#### k3s Installation
##### Host:
First install: `curl -sfL https://get.k3s.io | sh -`, then run with systemctl. Then use: `sudo cat /var/lib/rancher/k3s/server/node-token` to get the token, which will be used for networking.

##### vm:
`curl -sfL https://get.k3s.io | sh -s - agent  --server https://192.168.100.1:6443 --token <token obtained above>  --node-name worker-01`, this achieves networking. Alternatively, you can create your own `/etc/systemd/system/k3s-agent.service`, which will also be used when replacing the cri.

If networking is successful, you can test by running `kubectl get nodes` on the host:
```
NAME        STATUS     ROLES                  AGE   VERSION
master      Ready      control-plane,master   20d   v1.33.5+k3s1
worker-01   Ready      <none>                 16m   v1.33.5+k3s1
```

#### CRI Replacement:
Copy the compiled hvisor-cri to /root inside rootfs1.img, then create a systemd service
``` 
sudo tee /etc/systemd/system/hvisor-cri.service > /dev/null << 'EOF'
[Unit]
Description=Hvisor Container Runtime Interface
Documentation=###
After=network.target
Wants=network.target

[Service]
Type=simple
User=root
ExecStart=/root/hvisor-cri
Restart=always
RestartSec=5s
LimitNOFILE=infinity
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity

# Environment variables (configure as needed)
Environment=LOG_LEVEL=info
Environment=SOCKET_PATH=/var/run/hvisor-cri.sock

[Install]
WantedBy=multi-user.target
EOF
```

After `insmod hviosr.ko`, you can `systemctl start hvisor-cri`

```
sudo tee /etc/systemd/system/k3s-agent.service > /dev/null << 'EOF'
[Unit]
Description=Lightweight Kubernetes
Documentation=https://k3s.io
Wants=network-online.target
After=network-online.target

[Install]
WantedBy=multi-user.target

[Service]
Type=notify
EnvironmentFile=-/etc/default/%N
EnvironmentFile=-/etc/sysconfig/%N
EnvironmentFile=-/etc/systemd/system/k3s.service.env
KillMode=process
Delegate=yes
User=root
# Having non-zero Limit*s causes performance problems due to accounting overhead
# in the kernel. We recommend using cgroups to do container-local accounting.
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
TimeoutStartSec=0
Restart=always
RestartSec=5s
ExecStartPre=-/sbin/modprobe br_netfilter
ExecStartPre=-/sbin/modprobe overlay
ExecStart=/usr/local/bin/k3s \
    agent \
    --server=https://192.168.31.100:6443 \
    --token=K104a48343f2f1bb30d9e37c019766a9bc2a0432a59919da025dd165836b7b81593::server:d0000eb5dbc8e969fbd2290c39fbcd67 \
    --container-runtime-endpoint=unix:///var/run/hvisor-cri.sock \
    --image-service-endpoint=unix:///var/run/hvisor-cri.sock 
EOF
```

systemctl daemon-reload

#### Running Demo
The example directory contains three specific configuration files for different operating systems. Copy them to the agent node, then run `kubectl apply -f <config-file>` on the master node.
