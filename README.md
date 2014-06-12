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
