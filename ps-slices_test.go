package ps

import (
	"github.com/MeloQi/interfaces"
	"github.com/MeloQi/rtp"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestH264OnlyPsMuxSlices(t *testing.T) {
	/*	var fileName string = `D:\workspace\test.h264`
		var saveFileName string = `D:\workspace\test.mpeg`*/

	mux := NewH264OnlyPsMuxSlices()
	fReadbuf := make([]byte, BUF_LEN)
	pStaticBuf := make([]byte, 2*BUF_LEN)
	StaticBufSize := 0
	pts := int64(0)
	dts := int64(0)
	f, err := os.Open(fileName)
	if err != nil {
		t.Error(err)
		t.Fail()
		return
	}
	saveFile, err := os.Create(saveFileName)
	if err != nil {
		t.Error(err)
		t.Fail()
		return
	}

	socket, err := net.DialUDP("udp4", nil, &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 1234,
	})
	if err != nil {
		t.Error("连接失败!", err)
		t.Fail()
		return
	}
	defer socket.Close()

	rtpInst := rtp.NewRRtpTransfer()

	for {
		n, err := f.Read(fReadbuf)
		//t.Log("read ", n, " bytes")
		if err != nil {
			if err == io.EOF {
				return
			}
			t.Error(err)
			t.Fail()
			return
		}
		dst := pStaticBuf[StaticBufSize:]
		src := fReadbuf[:n]
		copy(dst, src)
		LastBlockLen := StaticBufSize + n
		LeaveLen := LastBlockLen
		pCurPos := pStaticBuf
		NaluStartPos := pStaticBuf
		FrameLen := 0
		isFirstFrame := true

		for LastBlockLen > 4 {
			if (isH264Or265Frame(pCurPos, nil)) {
				if isFirstFrame {
					isFirstFrame = false
				} else {
					frame := NaluStartPos[4:FrameLen]
					frameLen := len(frame)
					pFrame := frame
					pkg := &interfaces.Packet{IsMetadata: false, IsVideo: true, TimeStamp: uint32(pts / 90), RtpTimeStamp: uint32(pts), Data: []interfaces.FrameSlice{}, DataLen: frameLen}
					Type := getH264NALtype(frame[0])
					if Type == NAL_PPS || Type == NAL_SPS {
						rtpData := make([]byte, frameLen)
						copy(rtpData, pFrame)
						pkg.Data = append(pkg.Data, interfaces.FrameSlice{Data: rtpData, RtpData: nil})
					} else {
						rtpData := make([]byte, 1)
						copy(rtpData, []byte{frame[0]})
						frameLen -= 1
						pFrame = pFrame[1:]
						pkg.Data = append(pkg.Data, interfaces.FrameSlice{Data: rtpData, RtpData: nil})
						for frameLen > 0 {
							var rtpData []byte
							if frameLen > 1400 {
								rtpData = make([]byte, 1412)
								copy(rtpData[12:], pFrame[:1400])
								pFrame = pFrame[1400:]
								frameLen -= 1400
							} else {
								rtpData = make([]byte, len(pFrame)+12)
								copy(rtpData[12:], pFrame)
								frameLen -= len(pFrame)
							}
							pkg.Data = append(pkg.Data, interfaces.FrameSlice{Data: rtpData[12:], RtpData: rtpData})
						}
					}

					if out, err := mux.Mux(pkg, pts); err != nil {
						t.Error(err)
						t.Log("mux error!")
						t.Fail()
						return
					} else {
						if out != nil && len(out) > 0 {
							for i, v := range out {
								saveFile.Write(v.Data)
								d := v.RtpData
								if d == nil || len(d) == 0 {
									d = v.Data
								}
								rtpInst.PkgRtpOut(d, rtp.RTP_TYPE_VIDEO, v.RtpData != nil, 96, i == len(out)-1, uint32(dts), 11111, func(pack *rtp.RTPPack) {
									socket.Write(pack.Buffer.Bytes())
								})
							}
						}
					}

					if (Type == NAL_IDR) || (Type == NAL_PFRAME) {
						pts += 3600
						dts += 3600
						time.Sleep(time.Millisecond * 38)
					}
				}

				LeaveLen = LeaveLen - FrameLen
				NaluStartPos = pCurPos
				FrameLen = 0
				pCurPos = pCurPos[2:]
				LastBlockLen -= 2
				FrameLen += 2
			}
			FrameLen++
			pCurPos = pCurPos[1:]
			LastBlockLen--
		}

		dst = pStaticBuf[:LeaveLen]
		src = NaluStartPos[:LeaveLen]
		copy(dst, src)
		StaticBufSize = LeaveLen;
	}

	if f != nil {
		f.Close()
	}
	if saveFile != nil {
		saveFile.Close()
	}
}
