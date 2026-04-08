Param(
    [int]$Count = 3,
    [string]$BaseName = "Client",
    [int]$StartPort = 7001
)

Write-Host "Starting $Count client containers with base name $BaseName..."
docker compose build client

for ($i=1; $i -le $Count; $i++) {
    $id = "$BaseName`_$i"
    # ensure unique name
    $exists = docker ps -a --format "{{.Names}}" | Where-Object { $_ -eq $id }
    if ($exists) {
        $suffix = 1
        while (docker ps -a --format "{{.Names}}" | Where-Object { $_ -eq "$id`_dup$suffix" }) { $suffix++ }
        $id = "$id`_dup$suffix"
        Write-Host "Name exists, using $id"
    }

    # run client container (no-deps) to have multiple independent clients
    docker compose run -d --no-deps --name $id -e "CLIENT_ID=$id" client
    Write-Host "Started client $id"
}

Write-Host "Done. Use 'docker ps' to inspect clients and 'docker rm -f <name>' to stop them." 