// CI runs make targets and nothing else (see CLAUDE.md), so a green build
// here means the same thing as `make lint test` on a laptop.
//
// The agent image pins the same Go (1.27.1) and golangci-lint (2.14.0) as
// mise.toml; it also has make, git and gcc, which `-race` needs.
def AGENT_IMAGE = 'golangci/golangci-lint:v2.14.0@sha256:ad862ba6b3798cbe0fd9fd7408d498fd74fbd2623a92406b2fd3898faf0bf98f'

pipeline {
    agent none

    options {
        timeout(time: 30, unit: 'MINUTES')
        buildDiscarder(logRotator(numToKeepStr: '30'))
    }

    environment {
        // Fail instead of silently downloading a different Go.
        GOTOOLCHAIN = 'local'
    }

    stages {
        stage('Check') {
            agent {
                docker {
                    image AGENT_IMAGE
                    reuseNode true
                }
            }
            environment {
                // The agent runs as the Jenkins user, whose HOME is not
                // writable inside the container; keep caches in the workspace.
                GOCACHE             = "${WORKSPACE}/.cache/go-build"
                GOMODCACHE          = "${WORKSPACE}/.cache/go-mod"
                GOLANGCI_LINT_CACHE = "${WORKSPACE}/.cache/golangci-lint"
                // Go makes the module cache read-only by default, which
                // stops Jenkins from deleting the workspace later.
                GOFLAGS             = '-modcacherw'
            }
            stages {
                stage('Lint') {
                    steps { sh 'make lint' }
                }
                stage('Test') {
                    steps { sh 'make test' }
                }
                stage('Build') {
                    steps { sh 'make build' }
                }
                stage('Release binaries') {
                    when { buildingTag() }
                    steps {
                        sh 'make release VERSION="$TAG_NAME"'
                        archiveArtifacts artifacts: 'dist/*.tar.gz, dist/SHA256SUMS', fingerprint: true
                    }
                }
            }
        }

        stage('Container image') {
            when { buildingTag() }
            // Needs a node with Docker and buildx; label to suit your agents.
            agent { label 'docker' }
            steps {
                // Credentials ID is an assumption: a Docker Hub username and
                // access token with write access to the image repository.
                withDockerRegistry(credentialsId: 'dockerhub-push', url: '') {
                    sh 'make image-push VERSION="$TAG_NAME"'
                }
            }
        }
    }
}
