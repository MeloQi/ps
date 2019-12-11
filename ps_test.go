package ps

import (
	"io"
	"os"
	"testing"
)

const (
	fileName     string = `E:\workspace\1234.h264`
	saveFileName string = `E:\workspace\1234.mpeg`
)

func TestGb28181PsMux_MuxH264SingleFrame(t *testing.T) {
	gb28181PsMux := NewGb28181PsMux()
	idx := gb28181PsMux.AddStream(PSMUX_ST_VIDEO_H264)
	pMuxOutBuf := make([]byte, BUF_LEN)
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
					MuxOutSize := 0
					if err := gb28181PsMux.MuxH264SingleFrame(NaluStartPos, FrameLen,
						pts, dts, idx, pMuxOutBuf, &MuxOutSize, BUF_LEN); err != nil {
						t.Error(err)
						t.Log("mux error!")
						t.Fail()
						return
					}
					if MuxOutSize > 0 {
						data := pMuxOutBuf[:MuxOutSize]
						saveFile.Write(data)
					}

					c := uint8(0)
					if !isH264Or265Frame(NaluStartPos, &c) {
						t.Log("isH264Or265Frame is false")
						t.Fail()
						return
					}
					Type := getH264NALtype(c)
					if ((Type == NAL_IDR) || (Type == NAL_PFRAME)) {
						pts += 3600;
						dts += 3600;
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
