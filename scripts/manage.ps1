<#
Gerenciador simplificado com menu numérico.
O usuário escolhe opções digitando o número correspondente.
#>

function Start-Gateway { docker-compose up -d gateway }
function Start-Devices([int]$count) {
    if ($null -eq $count -or $count -le 0) { docker-compose up -d device } else { docker-compose up -d --scale device=$count }
}
function Start-Client { docker-compose up -d client }
function Logs-Gateway { docker-compose logs -f gateway }
function Logs-Device { docker-compose logs -f device }
function Logs-Client { docker attach client }
function Scale-Devices([int]$count) { docker-compose up -d --scale device=$count }

while ($true) {
    Clear-Host
    Write-Host "=== Gerenciador (Menu) ==="
    Write-Host "1) Start all (gateway + devices + client)"
    Write-Host "2) Start gateway"
    Write-Host "3) Start devices (escolher quantidade)"
    Write-Host "4) Start client"
    Write-Host "5) Logs gateway"
    Write-Host "6) Logs devices"
    Write-Host "7) Logs client (attach)"
    Write-Host "8) Scale devices (escolher quantidade)"
    Write-Host "9) Stop all"
    Write-Host "10) Spawn 10 devices (GPU_1..GPU_10)"
    Write-Host "11) Spawn 500 devices (teste de carga)"
    Write-Host "0) Exit"
    $choice = Read-Host 'Selecione uma opção (0-9)'
    switch ($choice) {
        '1' {
            $dc = Read-Host 'Quantos devices? (enter = 3)'
            if ([string]::IsNullOrWhiteSpace($dc)) { $dc = 3 } else { $dc = [int]$dc }
            Start-Gateway
            Start-Devices $dc
            Start-Client
            Write-Host 'Serviços iniciados. Use opção 5/6/7 para ver logs/attach.'
            Read-Host 'Pressione Enter para voltar ao menu.'
        }
        '2' { Start-Gateway; Write-Host 'Gateway iniciado. Use opção 5 para ver logs.'; Read-Host 'Pressione Enter para voltar ao menu.' }
        '3' {
            $dc = Read-Host 'Quantos devices?'
            if (-not [int]::TryParse($dc,[ref]$null)) { Write-Host 'Valor inválido'; Start-Sleep -Seconds 1; continue }
            Scale-Devices([int]$dc)
            Write-Host "Devices escalados para $dc. Use opção 6 para ver logs."
            Read-Host 'Pressione Enter para voltar ao menu.'
        }
        '4' { Start-Client; Write-Host 'Client iniciado. Use opção 7 para attach.'; Read-Host 'Pressione Enter para voltar ao menu.' }
        '5' { Write-Host 'Exibindo logs do gateway (Ctrl+C para interromper)...'; Logs-Gateway; Read-Host 'Pressione Enter para voltar ao menu.' }
        '6' { Write-Host 'Exibindo logs dos devices (Ctrl+C para interromper)...'; Logs-Device; Read-Host 'Pressione Enter para voltar ao menu.' }
        '7' { Write-Host 'Anexando ao client. Para sair use Ctrl+P Ctrl+Q (detach) ou Ctrl+C.'; Logs-Client; Read-Host 'Pressione Enter para voltar ao menu.' }
        '8' {
            $dc = Read-Host 'Quantos devices (para scale)?'
            if (-not [int]::TryParse($dc,[ref]$null)) { Write-Host 'Valor inválido'; Start-Sleep -Seconds 1; continue }
            Scale-Devices([int]$dc)
            Read-Host 'Pressione Enter para voltar ao menu.'
        }
        '10' {
            Write-Host 'Criando 10 devices com nomes GPU_1..GPU_10 (usando scripts/spawn_devices.ps1)...'
            & .\scripts\spawn_devices.ps1 -Count 10 -BaseName GPU -StartPort 6001
            Write-Host 'Spawn solicitado. Verifique com docker ps.'
            Read-Host 'Pressione Enter para voltar ao menu.'
        }
        '11' {
            Write-Host 'Iniciando teste de carga: 500 devices (scripts/test_500_devices.ps1)...'
            & .\scripts\test_500_devices.ps1 -Count 500 -BaseName GPU -StartPort 6001
            Write-Host 'Spawn 500 solicitado. Aguarde e verifique com docker ps.'
            Read-Host 'Pressione Enter para voltar ao menu.'
        }
        '9' { docker-compose down --remove-orphans; Read-Host 'Containers parados. Pressione Enter.' }
        '0' { break }
        default { Write-Host 'Opção inválida'; Start-Sleep -Seconds 1 }
    }
}
