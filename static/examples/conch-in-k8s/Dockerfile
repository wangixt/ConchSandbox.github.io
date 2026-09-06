FROM hub.oepkgs.net/openeuler/openeuler:24.03-lts-sp4

RUN dnf install -y ca-certificates iproute iptables iptables-nft util-linux containernetworking-plugins python3 python3-pip \
    gcc make autoconf automake libtool pkgconf-pkg-config \
    libuuid-devel zlib-devel lz4-devel xz-devel zstd-devel \
    libcurl-devel openssl-devel libxml2-devel json-c-devel \
    && dnf clean all

COPY erofs-utils-src /tmp/erofs-utils
RUN cd /tmp/erofs-utils \
    && ./autogen.sh \
    && ./configure --prefix=/usr \
    && make -j"$(getconf _NPROCESSORS_ONLN)" \
    && make install \
    && rm -rf /tmp/erofs-utils

COPY bin/conch bin/conchd bin/conch-init /usr/local/bin/
COPY bin/conch-kvm-device-plugin /usr/local/bin/conch-kvm-device-plugin
COPY bin/stratovirt /usr/bin/stratovirt
COPY bin/conch-init.cpio.gz bin/vmlinux.bin /opt/conch/
COPY bin/cni/ /usr/libexec/cni/
COPY conch-sdk/ /opt/conch/sdk/
COPY python-packages/ /opt/conch/python-packages/
COPY build-cold-template.sh start-sandbox.py /usr/local/bin/

ENV PYTHONPATH=/opt/conch/python-packages:/opt/conch/sdk
RUN chmod 0755 /usr/local/bin/build-cold-template.sh /usr/local/bin/start-sandbox.py

RUN mkdir -p /etc/conch/cni/net.d /var/run/conch /var/lib/conch \
    && printf '%s\n' 'app:' '  name: conch-k8s' 'server:' '  work_dir: /var/run/conch' '  state_dir: /var/lib/conch' 'sandbox:' '  backend: stratovirt' 'network:' '  warm_pool_size: 1' '  cni:' '    plugin_bin_dirs:' '      - /usr/libexec/cni' > /etc/conch/config.yaml \
    && printf '%s\n' '{"cniVersion":"1.0.0","name":"conch","type":"bridge","bridge":"cni-conch0","isGateway":true,"ipMasq":true,"ipam":{"type":"host-local","dataDir":"/var/lib/conch/cni","subnet":"10.13.0.0/24","routes":[{"dst":"0.0.0.0/0"}]},"dns":{"nameservers":["114.114.114.114","223.5.5.5"]}}' > /etc/conch/cni/net.d/10-conch.conf \
    && chmod 0600 /etc/conch/config.yaml \
    && chmod 0755 /usr/local/bin/conch /usr/local/bin/conchd /usr/local/bin/conch-init /usr/bin/stratovirt

ENTRYPOINT ["/usr/local/bin/conchd"]
CMD ["--config", "/etc/conch/config.yaml"]
