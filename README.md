# flysky-updater

A cross-platform open source command-line firmware updater for the FlySky i6.
Precompiled binaries for Windows and Linux can be found on the [releases page](https://github.com/mhils/flysky-updater/releases).

To my best understanding, it is not possible to accidentally brick the remote by flashing firmware; the initial bootloader is never overwritten.

**Usage:** Place a firmware image next to the updater and double-click the updater.  
Alternatively, use the command line interface:

```
λ ./flysky-updater-win64.exe --port COM3 --image flyplus.hex

0 B / 55.92 KB      [--------------------------------------------]   0.00 %
27.25 KB / 55.92 KB [===================>------------------------]  48.73 % 4s
55.92 KB / 55.92 KB [============================================] 100.00 % 7s

Upload completed.
Success!
```

## Firmware Update Mode

If the remote does not start anymore, one can access the firmware update mode as follows:

![](https://maximilianhils.com/upload/2016-02/i6firmwaremodepic.jpg)


## Protocol details

The serial communication between computer and remote follows a simple protocol.
Messages sent by the computer are constructed as follows:
```
length | payload | checksum
```
Messages sent by the remote are simply prefixed with `0x55`:
```
0x55 | length | payload | checksum
```

### Length
The message length is a little-endian two-byte integer. It accounts for the full message, including optional prefix and checksum.

### Payload
The payload is the actual message. From observing the original updater (see [serial-port-dump.txt](serial-port-dump.txt)), we can deduce the following message types (written in hexadecimal notation):

#### Ping Command
```
>> c0
<< c0 0a 00 01 00 00 00 00 00 00
```
The response seems to contain the firmware version, but I did not investigate this any further.

#### Restart Command
This command just restarts the remote, e.g. after a successful firmware update.
```
>> c1 00
```
#### "Can we write?" Command
The updater sends a "can we write now?" message every 1024 bytes, which, after confirmation, is followed by four write commands à 256 bytes.
```
>> c2 AD DR 00 09 00 00 00 00 00 00 00 00
<< c2 80 AD DR 00 09 00 00 00 00 00 00 00 00
```
where `AD DR` denotes the offset we want to start writing to (little-endian two-byte integer).

#### Write command
```
>> c3 AD DR 00 00 SI ZE DATA
<< c3 00 00 00 00
```
where `AD DR` is the offset (see above), `SI ZE` the number of bytes (little-endian two-byte integer, usually 256), and `DATA` the  bytes that should be written to memory.
If the updater does not receive a response quickly, it will re-send the write command. Interestingly, this results in a race condition in the original updater: If the updater does not receive a confirmation for `WRITE 0x1800` in time (the timeout here is very low), it will re-send the same command. The remote may however process both commands successfully and then return two confirmations. The updater treats the second confirmation for `0x1800` as a confirmation for the next offset, which however may not have been transmitted properly. This updater addresses the problem with very conservative timeouts.

### Checksum
The checksum is a little-endian two-byte integer that is computed as follows:
```python
checksum = 0xFFFF
for byte in payload:  # this includes prefix and length
	checksum -= byte
return checksum
```

### Example

The ping message is simply `c0` - including prefix and checksum it has a total length of 5: `05 00 c0 XX XX`  
We can now compute the checksum: `0xFFFF - 0x05 - 0x00 - 0xc0 = 0xff3a`  
Thus, the complete ping message is: `05 00 c0 3a ff`
