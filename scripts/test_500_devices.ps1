Param(
    [int]$Count = 500,
    [string]$BaseName = "GPU",
    [int]$StartPort = 6001,
    [int]$ActCount = 2
)

Write-Host "Test 500 devices: building image and starting $Count device containers..."
docker compose build device

# start in batches to avoid overwhelming Docker daemon
$batchSize = 50
$i = 1
while ($i -le $Count) {
    $end = [Math]::Min($i + $batchSize - 1, $Count)
    Write-Host "Starting devices $i to $end..."
    for ($j = $i; $j -le $end; $j++) {
        $id = "$BaseName`_$j"
        $port = $StartPort + $j - 1
        $exists = docker ps -a --format "{{.Names}}" | Where-Object { $_ -eq $id }
        if ($exists) {
            $suffix = 1
            while (docker ps -a --format "{{.Names}}" | Where-Object { $_ -eq "$id`_dup$suffix" }) { $suffix++ }
            $id = "$id`_dup$suffix"
        }
        docker compose run -d --no-deps --name $id -e "DEVICE_ID=$id" -e "DEVICE_CONTROL_PORT=$port" -e "DEVICE_ACT_COUNT=$ActCount" device | Out-Null
    }
    Start-Sleep -Seconds 3
    $i = $end + 1
}

Write-Host "All $Count device containers requested. Use 'docker ps' to inspect running containers." 