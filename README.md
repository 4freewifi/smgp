# Testing

	POST /json-rpc
	Content-Type: application/json; charset=utf-8

```json
{
  "jsonrpc": "2.0",
  "method": "SMGP.Submit",
  "params": [{
    "src": "02551744323",
    "dst": "13543810498",
    "msg": "测试测试"
  }],
  "id": 1
}
```

Expected response:

```json
{
    "result": {},
    "error": null,
    "id": 1
}
```

# ToDo

1. Submit failure retry
2. No response retry
3. Sliding windows

# License

Copyright 2015 John Lee <john@4free.com.tw>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
