FROM scratch

# GoReleaser puts us in a directory with the binary so we can reference it as a root file from 
# the Docker context.
COPY hegel /hegel
ENTRYPOINT ["/hegel"]