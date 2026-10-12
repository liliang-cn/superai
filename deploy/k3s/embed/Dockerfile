# The shared brain's embedding model, served on the hive's own nodes.
#
# CortexDB turns every recall query into a vector. That used to go through the
# t2m relay in Tokyo to Ollama on a laptop: one recall took 24 seconds because
# the laptop had pushed the model out of memory for another one. This is the
# same Ollama version as the Mac (0.35.1) and the same embeddinggemma file — vectors identical to the ones
# already stored — with the model baked in, so a node needs no network to
# start it. Built and loaded by deploy.sh; the model files come from the Mac's
# own Ollama store (models/ is filled by deploy.sh and not committed).
#
# CPU only: the official image is 6GB, nearly all of it GPU libraries these
# nodes have no GPU for, and an orange node has about 4GB of disk to spare.
FROM ollama/ollama:0.35.1 AS ollama
RUN rm -rf /usr/lib/ollama/cuda_* /usr/lib/ollama/mlx_cuda_* /usr/lib/ollama/vulkan /usr/lib/ollama/rocm*

FROM ubuntu:24.04
COPY --from=ollama /bin/ollama /bin/ollama
COPY --from=ollama /usr/lib/ollama /usr/lib/ollama
COPY models /root/.ollama/models
ENTRYPOINT ["/bin/ollama"]
CMD ["serve"]
ENV OLLAMA_HOST=0.0.0.0:11434 OLLAMA_KEEP_ALIVE=-1 OLLAMA_MAX_LOADED_MODELS=1 OLLAMA_NUM_PARALLEL=4
EXPOSE 11434
