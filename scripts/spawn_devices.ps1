Param(
    [int]$Count = 10,
    [string]$BaseName = "GPU",
    [int]$StartPort = 6001,
    [int]$ActCount = 2
)

Write-Host "Building device image..."
docker compose build device

for ($i=1; $i -le $Count; $i++) {
    $id = "$BaseName`_$i"
    $port = $StartPort + $i - 1
    # if name exists, append a suffix to avoid overwrite (keeps original index naming when possible)
    $exists = docker ps -a --format "{{.Names}}" | Where-Object { $_ -eq $id }
    if ($exists) {
        $suffix = 1
        while (docker ps -a --format "{{.Names}}" | Where-Object { $_ -eq "$id`_dup$suffix" }) { $suffix++ }
        $id = "$id`_dup$suffix"
        Write-Host "Nome já existe — usando nome alternativo: $id"
    }

    Write-Host "Starting device $id (control port $port)..."
    docker compose run -d --no-deps --name $id -e "DEVICE_ID=$id" -e "DEVICE_CONTROL_PORT=$port" -e "DEVICE_ACT_COUNT=$ActCount" device
}

Write-Host "Done. Started $Count devices with base name $BaseName."
Write-Host "To stop: docker rm -f <container-name> (see docker ps)"
