lines = [
    '@echo off',
    'setlocal',
    'taskkill /F /IM gowhale.exe 2>nul',
    'set GOCACHE=C:/temp/go-build-cache',
    'set GOMODCACHE=C:/Users/ms/go/pkg/mod',
    'cd /d D:/dev/code/go-whale',
    'go build -o C:/temp/gowhale_p.exe',
    'if %ERRORLEVEL% EQU 0 (',
    '    echo [compile] OK',
    '    copy /Y C:/temp/gowhale_p.exe C:/Users/ms/go/bin/gowhale.exe',
    '    echo.',
    '    echo === test ===',
    '    go test -count=1 ./internal/... -v',
    '    echo.',
    '    echo === done ===',
    ') else (',
    '    echo [compile] FAIL',
    ')',
    'pause',
]
with open(r'D:\dev\code\go-whale\rebuild.bat', 'w', encoding='ascii', newline='\r\n') as f:
    f.write('\n'.join(lines))
print('done')
