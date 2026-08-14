FROM quay.io/frrouting/frr:10.7.0

USER root
COPY snmpd.conf /etc/snmp/snmpd.conf
COPY docker-start.sh /usr/local/bin/bgpls-frr-start
RUN chmod +x /usr/local/bin/bgpls-frr-start \
    && if command -v apk >/dev/null 2>&1; then \
         apk add --no-cache net-snmp; \
       else \
         apt-get update \
         && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends snmpd \
         && rm -rf /var/lib/apt/lists/*; \
       fi

ENTRYPOINT ["/usr/local/bin/bgpls-frr-start"]
