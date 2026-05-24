# Single-command multi-target build. `docker buildx bake` (no args) reads this
# file, builds both the Linux and Windows Dockerfiles in parallel, and writes
# the resulting binaries to ./bin on the host.
#
#   docker buildx bake          # builds both -> ./bin/gramify and ./bin/gramify.exe
#   docker buildx bake linux    # one target
#   docker buildx bake windows

group "default" {
  targets = ["linux", "windows"]
}

target "linux" {
  dockerfile = "Dockerfile.linux"
  context    = "."
  output     = ["type=local,dest=./bin"]
}

target "windows" {
  dockerfile = "Dockerfile.windows"
  context    = "."
  output     = ["type=local,dest=./bin"]
}
