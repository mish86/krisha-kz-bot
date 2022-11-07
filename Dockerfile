FROM heroku/heroku:22-build as build

COPY . /app
WORKDIR /app

# Setup buildpack
RUN mkdir -p /tmp/buildpack/heroku/go /tmp/build_cache /tmp/env
RUN curl https://buildpack-registry.s3.amazonaws.com/buildpacks/heroku/go.tgz | tar xz -C /tmp/buildpack/heroku/go

#Execute Buildpack
RUN STACK=heroku-22 /tmp/buildpack/heroku/go/bin/compile /app /tmp/build_cache /tmp/env

# Prepare final, minimal image
FROM heroku/heroku:22

COPY --from=build /app /app
ENV HOME /app
WORKDIR /app
RUN chmod +x /app/scripts/start.sh
RUN useradd -m heroku
USER heroku
# cmd/<executable name> is not supported by heroku go buildpack
# https://github.com/heroku/heroku-buildpack-go#default-procfile
CMD /app/scripts/start.sh