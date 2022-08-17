# logmonitor [![Go](https://github.com/frjufvjn/logmonitor/actions/workflows/go.yml/badge.svg)](https://github.com/frjufvjn/logmonitor/actions/workflows/go.yml)
> 로그파일을 읽어서 특정 키워드 검출시 슬랙이나 잔디 알람 발송


## 설치 방법

OS X:

```sh
go build
```

Linux:
```
GOOS=linux go build -o ./bin/logmonitor.bin
```

윈도우:

```sh
GOOS=windows go build -o ./bin/logmonitor.exe
```

## 컴파일된 바이너리 이용
- ./bin/logmonitor : Mac에서 실행 가능
- ./bin/logmonitor.bin : 리눅스에서 실행 가능
- ./bin/logmonitor.exe : 윈도우에서 실행 가능

## CI/CD
> git master브랜치에서 push나 PR이 발생할 경우 github actions를 통해 serverless로 자동 빌드 되고 빌드된 리눅스용 바이너리 파일이 AWS 클라우드게이트 S3 버킷 (cloud-gate-temp)에 업로드 된다. 

## 사용 예제
1. 이 repository의 CI/CD에 의해 컴파일된 프로그램 실행 파일은 CG의 S3버킷에 저장되고 있다. 아래와 같이 해당 서버 인스턴스에서 S3의 파일을 받고 실행권한을 준다.
```
$ aws s3 cp s3://cloud-gate-temp/logmonitor ./logmonitor
$ chmod 755 ./logmonitor 
```
2. logmonitor-config.yml 파일을 목적에 맞게 수정한다. (파일명 바꾸면 안됨.)
```yaml
appName: "서버1" # 프로그램명 
awsEc2: false # AWS EC2 환경 여부
runCycleSec: 1 # 프로그램 실행주기 (초단위)
sameKeywordThreasholdSec: 30 # 동일한 검출된 키워드 발견시 알람을 skip하는 기간 (초단위)
sameKeywordExtractLen: 20 # 동일 키워드 검출 길이
filelist: # 대상 로그파일들 정보
  - id: application # 로그파일의 identification 
    path: /Users/jungwoopark/workspace/refrence/source/GO/frjufvjn/logmonitor/test.log # 로그파일 full 경로 
    keywords: # 키워드
      - pattern: ERROR
      - pattern: WARN
alarm:
  slack: # 슬랙 알람 설정
    enable: true # 슬랙 알람 사용여부 
    incommingWebhookUrl: "https://dddd"
  jandi: # 잔디 알람 설정
    enable: false # 잔디 알람 사용여부 
    incommingWebhookUrl: "https://wh.jandi.com/connect-api/webhook/999999999/xxxxxxxxxxxxxxxxxxxxxx"
```
3. 프로그램을 실행한다.
```
nohup ./logmonitor 1>/dev/null 2>&1 &
```
