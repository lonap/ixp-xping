package sockettimestamp

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func EnableRXTimestampsOnSocket(uconn *net.UDPConn) error {
	file, err := uconn.File()
	if err != nil {
		return err
	}

	fd := file.Fd()
	err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TIMESTAMP, 1)
	if err != nil {
		return err
	}

	err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SOF_TIMESTAMPING_RX_SOFTWARE, 1)
	if err != nil {
		return err
	}

	return nil
}

func DecodeRXTimestampFromOOB(oobData []byte) (t time.Time, err error) {
	parts, err := syscall.ParseSocketControlMessage(oobData)
	if err != nil {
		return time.Time{}, err
	}

	for _, part := range parts {
		if part.Header.Type == unix.SO_TIMESTAMP &&
			part.Header.Level == unix.SOL_SOCKET {
			/*
				2.1 SCM_TIMESTAMPING records

				These timestamps are returned in a control message with cmsg_level
				SOL_SOCKET, cmsg_type SCM_TIMESTAMPING, and payload of type

				For SO_TIMESTAMPING_OLD:

				struct scm_timestamping {
					struct timespec ts[3];
				};

				For SO_TIMESTAMPING_NEW:

				struct scm_timestamping64 {
					struct __kernel_timespec ts[3];

				Always use SO_TIMESTAMPING_NEW timestamp to always get timestamp in
				struct scm_timestamping64 format.

				SO_TIMESTAMPING_OLD returns incorrect timestamps after the year 2038
				on 32 bit machines.

				The structure can return up to three timestamps. This is a legacy
				feature. At least one field is non-zero at any time. Most timestamps
				are passed in ts[0]. Hardware timestamps are passed in ts[2].
			*/

			// b := bytes.NewReader(part.Data)
			ts64, ts64nsec := uint64(0), uint64(0)
			ts64 = binary.NativeEndian.Uint64(part.Data[:8])
			ts64nsec = binary.NativeEndian.Uint64(part.Data[8:16])
			return time.Unix(int64(ts64), int64(ts64nsec)*1000), nil
		}
	}

	log.Printf("%#v", parts)

	return time.Time{}, fmt.Errorf("no time data found")
}
