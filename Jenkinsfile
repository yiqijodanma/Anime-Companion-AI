pipeline {
    agent { label 'anime-companion-builder' }

    options {
        disableConcurrentBuilds()
        timeout(time: 90, unit: 'MINUTES')
        timestamps()
        skipDefaultCheckout(true)
    }

    stages {
        stage('Checkout') {
            steps {
                dir('backend') {
                    checkout scm
                }
                dir('frontend') {
                    checkout([
                        $class: 'GitSCM',
                        branches: [[name: 'refs/remotes/origin/main']],
                        extensions: [[
                            $class: 'CloneOption',
                            depth: 1,
                            honorRefspec: true,
                            noTags: true,
                            reference: '',
                            shallow: true,
                        ]],
                        userRemoteConfigs: [[
                            refspec: '+refs/heads/main:refs/remotes/origin/main',
                            url: 'https://github.com/yiqijodanma/Anime-Companion-ai-sos-chat-fronted.git',
                        ]],
                    ])
                }
            }
        }

        stage('Backend tests') {
            steps {
                dir('backend') {
                    sh 'GOMAXPROCS=1 GOMEMLIMIT=256MiB go test -p 1 ./...'
                }
            }
        }

        stage('Frontend build') {
            steps {
                dir('frontend') {
                    sh 'npm ci --no-audit --no-fund'
                    sh 'NODE_OPTIONS=--max-old-space-size=256 npm run build'
                }
            }
        }

        stage('Deploy production') {
            when {
                branch 'main'
            }
            steps {
                script {
                    env.DEPLOY_TEMP_DIR = sh(
                        label: 'Create private deployment directory',
                        returnStdout: true,
                        script: '''
                            set -eu
                            umask 077
                            mktemp -d '/tmp/anime-companion-jenkins.XXXXXX'
                        '''
                    ).trim()
                }
                withCredentials([
                    usernamePassword(
                        credentialsId: 'acr-push',
                        usernameVariable: 'ACR_USERNAME',
                        passwordVariable: 'ACR_PASSWORD'
                    ),
                    sshUserPrivateKey(
                        credentialsId: 'k3s-tunnel-ssh',
                        keyFileVariable: 'SSH_PRIVATE_KEY',
                        usernameVariable: 'SSH_USERNAME'
                    ),
                    file(credentialsId: 'k3s-deployer-kubeconfig', variable: 'KUBECONFIG_SOURCE'),
                    file(credentialsId: 'server-known-hosts', variable: 'SERVER_KNOWN_HOSTS'),
                    file(credentialsId: 'anime-companion-release-parameters', variable: 'RELEASE_PARAMETERS'),
                ]) {
                    sh(label: 'Release through the k3s API tunnel', script: '''
                        set -eu
                        set +x

                        if [ "$SSH_USERNAME" != 'jenkins-k3s-tunnel' ]; then
                            echo 'k3s-tunnel-ssh must use the jenkins-k3s-tunnel account.' >&2
                            exit 1
                        fi

                        target='jenkins-k3s-tunnel@20.78.58.0'
                        control_socket="$DEPLOY_TEMP_DIR/ssh-control"
                        kubeconfig="$DEPLOY_TEMP_DIR/kubeconfig"
                        known_hosts="$DEPLOY_TEMP_DIR/known_hosts"
                        tunnel_pid=''

                        close_stage_tunnel() {
                            status=$?
                            trap - EXIT HUP INT TERM
                            if [ -n "$tunnel_pid" ] && kill -0 "$tunnel_pid" 2>/dev/null; then
                                ssh -S "$control_socket" -O exit "$target" >/dev/null 2>&1 || \
                                    kill "$tunnel_pid" >/dev/null 2>&1 || true
                                wait "$tunnel_pid" 2>/dev/null || true
                            fi
                            exit "$status"
                        }
                        trap close_stage_tunnel EXIT
                        trap 'exit 129' HUP
                        trap 'exit 130' INT
                        trap 'exit 143' TERM

                        cp "$KUBECONFIG_SOURCE" "$kubeconfig"
                        cp "$SERVER_KNOWN_HOSTS" "$known_hosts"
                        chmod 600 "$kubeconfig" "$known_hosts"

                        ssh -M -S "$control_socket" -NT \
                            -L 127.0.0.1:16443:127.0.0.1:6443 \
                            -o BatchMode=yes \
                            -o ControlPersist=no \
                            -o ExitOnForwardFailure=yes \
                            -o IdentitiesOnly=yes \
                            -o ServerAliveInterval=30 \
                            -o ServerAliveCountMax=3 \
                            -o StrictHostKeyChecking=yes \
                            -o UserKnownHostsFile="$known_hosts" \
                            -i "$SSH_PRIVATE_KEY" \
                            "$target" &
                        tunnel_pid=$!
                        tunnel_ready='false'
                        for attempt in $(seq 1 30); do
                            if [ -S "$control_socket" ] && \
                                ssh -S "$control_socket" -O check "$target" >/dev/null 2>&1; then
                                tunnel_ready='true'
                                break
                            fi
                            if ! kill -0 "$tunnel_pid" 2>/dev/null; then
                                wait "$tunnel_pid"
                                echo 'The restricted K3s SSH tunnel exited before becoming ready.' >&2
                                exit 1
                            fi
                            sleep 1
                        done
                        if [ "$tunnel_ready" != 'true' ]; then
                            echo 'The restricted K3s SSH tunnel did not become ready in time.' >&2
                            exit 1
                        fi

                        export KUBECONFIG="$kubeconfig"
                        cluster_name="$(kubectl config view --minify --raw -o 'jsonpath={.contexts[0].context.cluster}')"
                        if [ -z "$cluster_name" ]; then
                            echo 'The deployer kubeconfig has no current cluster.' >&2
                            exit 1
                        fi
                        kubectl config set-cluster "$cluster_name" --server='https://127.0.0.1:16443' >/dev/null
                        configured_server="$(kubectl config view --minify --raw -o 'jsonpath={.clusters[0].cluster.server}')"
                        if [ "$configured_server" != 'https://127.0.0.1:16443' ]; then
                            echo 'The deployer kubeconfig was not restricted to the local tunnel.' >&2
                            exit 1
                        fi

                        export DOCKER_CONFIG="$DEPLOY_TEMP_DIR/docker-config"
                        mkdir -m 700 "$DOCKER_CONFIG"
                        registry_host="$(pwsh -NoProfile -NonInteractive -File "$WORKSPACE/backend/scripts/release/Deploy-Jenkins.ps1" \
                            -ReleaseParametersPath "$RELEASE_PARAMETERS" \
                            -BackendPath "$WORKSPACE/backend" \
                            -FrontendPath "$WORKSPACE/frontend" \
                            -RegistryHostOnly)"
                        case "$registry_host" in
                            ''|*[!A-Za-z0-9.:-]*)
                                echo 'The validated registry hostname is unsafe.' >&2
                                exit 1
                                ;;
                        esac
                        printf '%s\n' "$registry_host" > "$DEPLOY_TEMP_DIR/registry-host"
                        printf '%s' "$ACR_PASSWORD" | docker login "$registry_host" \
                            --username "$ACR_USERNAME" --password-stdin

                        pwsh -NoProfile -NonInteractive -File "$WORKSPACE/backend/scripts/release/Deploy-Jenkins.ps1" \
                            -ReleaseParametersPath "$RELEASE_PARAMETERS" \
                            -BackendPath "$WORKSPACE/backend" \
                            -FrontendPath "$WORKSPACE/frontend"
                    ''')
                }
            }
        }
    }

    post {
        always {
            sh(label: 'Clean deployment credentials and tunnel', script: '''
                set +x
                if [ -z "${DEPLOY_TEMP_DIR:-}" ]; then
                    exit 0
                fi
                case "$DEPLOY_TEMP_DIR" in
                    /tmp/anime-companion-jenkins.*) ;;
                    *)
                        echo 'Refusing to clean an unexpected deployment directory.' >&2
                        exit 1
                        ;;
                esac

                control_socket="$DEPLOY_TEMP_DIR/ssh-control"
                if [ -S "$control_socket" ]; then
                    ssh -S "$control_socket" -O exit 'jenkins-k3s-tunnel@20.78.58.0' >/dev/null 2>&1 || true
                fi

                if [ -r "$DEPLOY_TEMP_DIR/registry-host" ]; then
                    IFS= read -r registry_host < "$DEPLOY_TEMP_DIR/registry-host" || true
                    case "${registry_host:-}" in
                        ''|*[!A-Za-z0-9.:-]*) ;;
                        *) DOCKER_CONFIG="$DEPLOY_TEMP_DIR/docker-config" docker logout "$registry_host" >/dev/null 2>&1 || true ;;
                    esac
                fi

                rm -rf -- "$DEPLOY_TEMP_DIR"
            ''')
        }
    }
}
