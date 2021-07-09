FROM debian:bullseye
COPY rad-unnumbered /usr/sbin
ENTRYPOINT ["/usr/sbin/rad-unnumbered"]
