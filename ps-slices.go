package ps

import (
	"fmt"
	"github.com/MeloQi/interfaces"
	"github.com/MeloQi/rtp"
)

/*const (
	BUF_LEN int = (1024 * 1024)
)*/

type H264OnlyPsMuxSlices struct {
	pMuxOutBuf   []byte
	gb28181PsMux *Gb28181PsMuxSlices
	idx          int
}

func NewH264OnlyPsMuxSlices() *H264OnlyPsMuxSlices {
	m := &H264OnlyPsMuxSlices{pMuxOutBuf: make([]byte, BUF_LEN), gb28181PsMux: NewGb28181PsMuxSlices()}
	m.idx = m.gb28181PsMux.AddStream(PSMUX_ST_VIDEO_H264)
	return m
}

func (m *H264OnlyPsMuxSlices) Mux(frameSlices *interfaces.Packet, pts int64) ([]interfaces.FrameSlice, error) {
	pMuxOutSize := 0
	return m.gb28181PsMux.MuxH264SingleFrame(frameSlices, pts, pts, m.idx, m.pMuxOutBuf, &pMuxOutSize, BUF_LEN)
}

//Gb28181PsMux class
type Gb28181PsMuxSlices struct {
	mPsmuxcontext          *PsMuxSlices
	mVecstream             []*PsMuxSlicesStream
	spsPPSIDRCache         []interfaces.FrameSlice
	spsPPSIDRCachepOutSize int
}

func NewGb28181PsMuxSlices() *Gb28181PsMuxSlices {
	return &Gb28181PsMuxSlices{mPsmuxcontext: NewPsMuxSlices(),
		mVecstream: []*PsMuxSlicesStream{}}
}

func (g *Gb28181PsMuxSlices) AddStream(Type uint8) int {
	if g.mPsmuxcontext == nil {
		g.mPsmuxcontext = NewPsMuxSlices()
	}
	pStream := g.mPsmuxcontext.psmuxCreateStream(Type)
	g.mVecstream = append(g.mVecstream, pStream)
	return len(g.mVecstream) - 1
}

//输入单个H264/5帧,必须以00 00 00 01或者00 00 01开头,SPS PPS 和 I帧不能连在一起
func (g *Gb28181PsMuxSlices) MuxH264SingleFrame(frameSlices *interfaces.Packet, Pts, Dts int64, Idx int,
	outBuf []byte, pOutSize *int, maxOutSize int) ([]interfaces.FrameSlice, error) {
	*pOutSize = 0
	if Idx >= len(g.mVecstream) {
		return nil, fmt.Errorf("Idx >= m_VecStream size")
	}
	if frameSlices.DataLen < 2 {
		return nil, fmt.Errorf("MuxH264SingleFrame incomming data length < 2")
	}

	Type := getH264NALtype(frameSlices.Data[0].Data[0])
	if Type == NAL_other {
		return nil, fmt.Errorf("NAL Type Err")
	}
	if Type == NAL_SEI {
		return nil, nil
	}

	pMuxStream := g.mVecstream[Idx]

	//default
	g.mPsmuxcontext.enablePackHdr = false
	g.mPsmuxcontext.enablePsm = false
	g.mPsmuxcontext.enableSysHdr = false
	pMuxStream.pi.flags &= ^PSMUX_PACKET_FLAG_PES_DATA_ALIGN
	g.mPsmuxcontext.pts = Pts

	if Pts == Dts {
		Dts = INVALID_TS
	}

	if Type == NAL_PFRAME {
		g.mPsmuxcontext.enablePackHdr = true
		pMuxStream.pi.flags |= PSMUX_PACKET_FLAG_PES_DATA_ALIGN
	} else {
		//如果是单个SPS PPS 则等到I帧一起发送,原则就是同一个时间戳作为一个RTP包
		outBuf = outBuf[g.spsPPSIDRCachepOutSize:]
		maxOutSize -= g.spsPPSIDRCachepOutSize
		if Type == NAL_SPS {
			g.mPsmuxcontext.enablePackHdr = true
			g.mPsmuxcontext.enablePsm = true
			g.mPsmuxcontext.enableSysHdr = true
			Pts = INVALID_TS
			Dts = INVALID_TS
		} else if Type == NAL_PPS {
			Pts = INVALID_TS
			Dts = INVALID_TS
		} else if Type == NAL_IDR {
			pMuxStream.pi.flags |= PSMUX_PACKET_FLAG_PES_DATA_ALIGN
		}
	}

	if slices, err := g.mPsmuxcontext.psmuxMuxFrame(g.mVecstream[Idx], frameSlices, Pts, Dts, outBuf, pOutSize, maxOutSize); err != nil {
		return nil, err
	} else {
		if Type == NAL_SPS || Type == NAL_PPS {
			g.spsPPSIDRCachepOutSize += *pOutSize
			g.spsPPSIDRCache = append(g.spsPPSIDRCache, slices...)
			return nil, nil
		} else if Type == NAL_IDR {
			g.spsPPSIDRCachepOutSize = 0
			g.spsPPSIDRCache = append(g.spsPPSIDRCache, slices...)
			slices = g.spsPPSIDRCache
			g.spsPPSIDRCache = []interfaces.FrameSlice{}
		}
		return slices, nil
	}

}

//PsMux class
type PsMuxSlices struct {
	streams []*PsMuxSlicesStream
	idInfo  PsMuxStreamIdInfo

	pts     int64
	bitRate uint64
	/* bounds in system header */
	audioBound uint8
	videoBound uint8
	rateBound  uint64
	packetBuf  []byte
	esInfoBuf  []byte

	enablePackHdr bool
	packHdrPts    int64
	enableSysHdr  bool
	sysHdrPts     int64
	enablePsm     bool
	psmPts        int64
	pesCnt        uint32
	bitSize       uint64 /* accumulated bit size of processed data */
	bitPts        int64  /* last time the bit_rate is updated */
}

func NewPsMuxSlices() *PsMuxSlices {
	return &PsMuxSlices{packetBuf: make([]byte, PSMUX_MAX_PACKET_LEN),
		esInfoBuf:  make([]byte, PSMUX_MAX_ES_INFO_LENGTH),
		streams:    []*PsMuxSlicesStream{},
		pts:        INVALID_TS,
		packHdrPts: INVALID_TS,
		sysHdrPts:  INVALID_TS,
		psmPts:     INVALID_TS,
		bitPts:     0,
		bitRate:    BIT_RATE,
		rateBound:  (2 * BIT_RATE) / 400,
		videoBound: 0,
		audioBound: 0,
		idInfo: PsMuxStreamIdInfo{id_mpga: PSMUX_STREAM_ID_MPGA_INIT,
			id_mpgv:  PSMUX_STREAM_ID_MPGV_INIT,
			id_ac3:   PSMUX_STREAM_ID_AC3_INIT,
			id_spu:   PSMUX_STREAM_ID_SPU_INIT,
			id_dts:   PSMUX_STREAM_ID_DTS_INIT,
			id_lpcm:  PSMUX_STREAM_ID_LPCM_INIT,
			id_dirac: PSMUX_STREAM_ID_DIRAC_INIT}}
}

func (mux *PsMuxSlices) psmuxCreateStream(stream_type uint8) *PsMuxSlicesStream {
	if len(mux.streams) >= MAX_MUX_STREAM_NUM {
		return nil
	}

	stream := NewPsmuxSlicesStream(mux, stream_type)
	mux.streams = append(mux.streams, stream)

	if stream.isVideoStream {
		mux.videoBound++;
		if mux.videoBound > 32 {
			fmt.Printf("Number of video es exceeds upper limit")
		}
	} else if stream.isAudioStream {
		mux.audioBound++;
		if mux.audioBound > 64 {
			fmt.Printf("Number of audio es exceeds upper limit")
		}
	}

	return stream;
}

func (mux *PsMuxSlices) psmuxMuxFrame(stream *PsMuxSlicesStream, frameSlices *interfaces.Packet, pts, dts int64,
	outBuf []byte, pOutSize *int, maxOutSize int) ([]interfaces.FrameSlice, error) {
	pOutBuf := outBuf
	var outFrameSlices []interfaces.FrameSlice

	if stream == nil || pOutBuf == nil || maxOutSize < rtp.RTP_HEADER_LEN+4 {
		return nil, fmt.Errorf("bad parameter")
	}
	maxOutSize -= rtp.RTP_HEADER_LEN
	*pOutSize += rtp.RTP_HEADER_LEN
	pOutBuf = pOutBuf[rtp.RTP_HEADER_LEN:]

	{
		ts := stream.psMuxStreamGetPts();
		if ts != INVALID_TS {
			mux.pts = ts;
		}
	}

	if mux.enablePackHdr {
		if mux.pts != INVALID_TS && mux.pts > mux.bitPts && mux.pts-mux.bitPts > int64(PSMUX_BITRATE_CALC_INTERVAL) {
			/* XXX: smoothing the rate? */
			mux.bitRate = uint64((float64(mux.bitSize) * 8 * CLOCKBASE) / float64(mux.pts-mux.bitPts))
			mux.bitSize = 0
			mux.bitPts = mux.pts
		}
		muxHdrSize := 0
		if err := mux.psmuxWritePackHeader(pOutBuf, &muxHdrSize, maxOutSize); err != nil {
			return nil, err
		}
		maxOutSize -= muxHdrSize
		*pOutSize += muxHdrSize
		pOutBuf = pOutBuf[muxHdrSize:]
		mux.packHdrPts = mux.pts
	}

	if mux.enableSysHdr {
		muxSysHdrSize := 0
		if err := mux.psmuxWriteSystemHeader(pOutBuf, &muxSysHdrSize, maxOutSize); err != nil {
			return nil, err
		}
		maxOutSize -= muxSysHdrSize
		*pOutSize += muxSysHdrSize
		pOutBuf = pOutBuf[muxSysHdrSize:]
		mux.sysHdrPts = mux.pts
	}

	if mux.enablePsm {
		muxPsmSize := 0
		if err := mux.psmuxWriteProgramStreamMap(pOutBuf, &muxPsmSize, maxOutSize); err != nil {
			return nil, err
		}
		maxOutSize -= muxPsmSize
		*pOutSize += muxPsmSize
		pOutBuf = pOutBuf[muxPsmSize:]
		mux.psmPts = mux.pts
	}

	if *pOutSize > rtp.RTP_HEADER_LEN {
		outFrameSlices = append(outFrameSlices, interfaces.FrameSlice{Data: outBuf[rtp.RTP_HEADER_LEN:*pOutSize], RtpData: outBuf[:*pOutSize]})
	}

	PesSize := 0
	if frames, err := stream.psmuxStreamMuxFrame(frameSlices, pts, dts, pOutBuf, &PesSize, maxOutSize); err != nil {
		return nil, err
	} else {
		outFrameSlices = append(outFrameSlices, frames...)
	}
	*pOutSize += PesSize;
	mux.pesCnt += 1

	return outFrameSlices, nil
}

func (mux *PsMuxSlices) psmuxWritePackHeader(outBuf []byte, pOutSize *int, maxOutSize int) error {
	scr := mux.pts /* XXX: is this correct? necessary to put any offset? */
	if mux.pts == -1 {
		scr = 0
	}

	/* pack_start_code */
	bw := bitsInit(14, mux.packetBuf)
	bitsWrite(bw, 24, PSMUX_START_CODE_PREFIX)
	bitsWrite(bw, 8, PSMUX_PACK_HEADER)

	/* scr */
	bitsWrite(bw, 2, 0x1)
	bitsWrite(bw, 3, uint64(scr>>30)&0x07)
	bitsWrite(bw, 1, 1)
	bitsWrite(bw, 15, uint64(scr>>15)&0x7fff)
	bitsWrite(bw, 1, 1)
	bitsWrite(bw, 15, uint64(scr)&0x7fff)
	bitsWrite(bw, 1, 1)
	bitsWrite(bw, 9, 0) /* system_clock_reference_extension: set to 0 (like what VLC does) */
	bitsWrite(bw, 1, 1)
	{
		/* Scale to get the muxRate, rounding up */
		muxRate := (mux.bitRate + 8*50 - 1) / (8 * 50)
		//gst_util_uint64_scale (mux->bitRate + 8 * 50 - 1, 1, 8 * 50);
		if muxRate > mux.rateBound/2 {
			mux.rateBound = muxRate * 2
		}

		bitsWrite(bw, 22, muxRate) /* program_mux_rate */
		bitsWrite(bw, 2, 3)
	}

	bitsWrite(bw, 5, 0x1f)
	bitsWrite(bw, 3, 0) /* pack_stuffing_length */

	if maxOutSize < 14 {
		return fmt.Errorf("maxOutSize < 14")
	}
	data := bw.pData[:14]
	copy(outBuf, data)
	if pOutSize != nil {
		*pOutSize = 14
	}

	return nil
}

func (mux *PsMuxSlices) psmuxWriteSystemHeader(outBuf []byte, pOutSize *int, maxOutSize int) error {
	i := 12 + len(mux.streams)*3

	/* system_header_start_code */
	bw := bitsInit(i, mux.packetBuf)

	/* system_header start code */
	bitsWrite(bw, 24, PSMUX_START_CODE_PREFIX)
	bitsWrite(bw, 8, PSMUX_SYSTEM_HEADER)
	bitsWrite(bw, 16, uint64(i-6))           /* header_length */
	bitsWrite(bw, 1, 1)                      /* marker */
	bitsWrite(bw, 22, mux.rateBound)         /* rate_bound */
	bitsWrite(bw, 1, 1)                      /* marker */
	bitsWrite(bw, 6, uint64(mux.audioBound)) /* audio_bound */
	bitsWrite(bw, 1, 0)                      /* fixed_flag */
	bitsWrite(bw, 1, 0)                      /* CSPS_flag */
	bitsWrite(bw, 1, 0)                      /* system_audio_lock_flag */
	bitsWrite(bw, 1, 0)                      /* system_video_lock_flag */
	bitsWrite(bw, 1, 1)                      /* marker */
	bitsWrite(bw, 5, uint64(mux.videoBound)) /* video_bound */
	bitsWrite(bw, 1, 0)                      /* packet_rate_restriction_flag */
	bitsWrite(bw, 7, 0x7f)                   /* reserved_bits */

	for _, stream := range mux.streams {
		bitsWrite(bw, 8, uint64(stream.streamID)) /* stream_id */
		bitsWrite(bw, 2, 0x3)                     /* reserved */
		v := 0
		d := 128
		if stream.isVideoStream {
			v = 1
			d = 1024
		}
		bitsWrite(bw, 1, uint64(v))                       /* buffer_bound_scale */
		bitsWrite(bw, 13, stream.maxBufferSize/uint64(d)) /* buffer_size_bound */
	}

	if maxOutSize < i {
		return fmt.Errorf("maxOutSize < %d", i)
	}
	data := bw.pData[:i]
	copy(outBuf, data)
	if pOutSize != nil {
		*pOutSize = i
	}

	return nil
}

func (mux *PsMuxSlices) psmuxWriteProgramStreamMap(outBuf []byte, pOutSize *int, maxOutSize int) error {
	psmSize := 16
	esMapSize := 0
	i := 0

	/* pre-write the descriptor loop */
	pos := mux.esInfoBuf
	for _, stream := range mux.streams {
		i = 0
		pos[0] = stream.streamType
		pos[1] = stream.streamID
		pos = pos[2:]
		stream.psMuxStreamGetEsDescrs(pos[2:], &i)
		pos = psmux_put16(pos, uint16(i))
		esMapSize += i + 4
		pos = pos[i:]
	}

	psmSize += esMapSize
	bw := bitsInit(psmSize, mux.packetBuf)

	/* psm start code */
	bitsWrite(bw, 24, PSMUX_START_CODE_PREFIX)
	bitsWrite(bw, 8, PSMUX_PROGRAM_STREAM_MAP)
	bitsWrite(bw, 16, uint64(psmSize-6)) /* psm_length */
	bitsWrite(bw, 1, 1)                  /* current_next_indicator */
	bitsWrite(bw, 2, 0xF)                /* reserved */
	bitsWrite(bw, 5, 0x1)                /* psm_version = 1 */
	bitsWrite(bw, 7, 0xFF)               /* reserved */
	bitsWrite(bw, 1, 1)                  /* marker */

	bitsWrite(bw, 16, 0) /* program_stream_info_length */
	/* program_stream_info empty */

	bitsWrite(bw, 16, uint64(esMapSize)) /* elementary_stream_map_length */
	dst := bw.pData[bw.iData:]
	src := mux.esInfoBuf[:esMapSize]
	copy(dst, src)

	/* CRC32 */
	{
		crc := calc_crc32(mux.packetBuf[:psmSize-4])
		pos := mux.packetBuf[psmSize-4:]
		pos = psmux_put32(pos, crc);
	}

	if maxOutSize < psmSize {
		return fmt.Errorf("maxOutSize < %d", psmSize)
	}
	data := bw.pData[:psmSize]
	copy(outBuf, data)
	if pOutSize != nil {
		*pOutSize = psmSize
	}

	return nil
}

//PsMuxStream class
type PsMuxSlicesStream struct {
	pi PsMuxPacketInfo

	streamType  uint8
	streamID    uint8
	streamIdExt uint8

	/* PES payload */
	curPesPayloadSize uint16
	pesBytesWritten   uint16 /* delete*/

	/* stream type */
	isVideoStream bool
	isAudioStream bool

	/* PTS/DTS to write if the flags in the packet info are set */
	pts     int64 /* TODO: cur_buffer->pts?*/
	dts     int64 /* TODO: cur_buffer->dts?*/
	lastPts int64

	/* for writing descriptors */
	audioSampling int
	audioChannels int
	audioBitrate  int

	/* for writing buffer size in system header */
	maxBufferSize uint64
}

//NewPsmuxSlicesStream
func NewPsmuxSlicesStream(mux *PsMuxSlices, stream_type uint8) *PsMuxSlicesStream {
	info := &(mux.idInfo)
	stream := &PsMuxSlicesStream{
		streamType:    stream_type,
		isAudioStream: false,
		isVideoStream: false,
		streamID:      0,
		maxBufferSize: 0,
	}

	switch stream_type {
	/* MPEG AUDIO */
	case PSMUX_ST_PS_AUDIO_G711A:
	case PSMUX_ST_AUDIO_MPEG1:
	case PSMUX_ST_AUDIO_MPEG2:
		stream.maxBufferSize = 2484; /* ISO/IEC 13818 2.5.2.4 */
	case PSMUX_ST_AUDIO_AAC:
		if info.id_mpga > PSMUX_STREAM_ID_MPGA_MAX {
			break
		}
		stream.streamID = info.id_mpga
		info.id_mpga++
		stream.streamIdExt = 0
		stream.isAudioStream = true
		break
		/* MPEG VIDEO */
	case PSMUX_ST_VIDEO_MPEG1:
	case PSMUX_ST_VIDEO_MPEG2:
	case PSMUX_ST_VIDEO_MPEG4:
	case PSMUX_ST_VIDEO_H264:
		if info.id_mpgv > PSMUX_STREAM_ID_MPGV_MAX {
			break
		}
		stream.streamID = info.id_mpgv
		info.id_mpgv++
		stream.streamIdExt = 0
		stream.isVideoStream = true
		break
		/* AC3 / A52 */
	case PSMUX_ST_PS_AUDIO_AC3:
		if info.id_ac3 > PSMUX_STREAM_ID_AC3_MAX {
			break
		}
		stream.streamID = uint8(PSMUX_PRIVATE_STREAM_1)
		stream.streamIdExt = info.id_ac3
		info.id_ac3++
		stream.isAudioStream = true
		/* AC3 requires data alignment */
		stream.pi.flags |= PSMUX_PACKET_FLAG_PES_DATA_ALIGN
		break
		/* DTS */
	case PSMUX_ST_PS_AUDIO_DTS:
		if info.id_dts > PSMUX_STREAM_ID_DTS_MAX {
			break
		}
		stream.streamID = uint8(PSMUX_PRIVATE_STREAM_1)
		stream.streamIdExt = info.id_dts
		info.id_dts++
		stream.isAudioStream = true
		break
		/* LPCM */
	case PSMUX_ST_PS_AUDIO_LPCM:
		if info.id_lpcm > PSMUX_STREAM_ID_LPCM_MAX {
			break
		}
		stream.streamID = uint8(PSMUX_PRIVATE_STREAM_1)
		stream.streamIdExt = info.id_lpcm
		info.id_lpcm++
		stream.isAudioStream = true
		break
	case PSMUX_ST_VIDEO_DIRAC:
		if info.id_dirac > PSMUX_STREAM_ID_DIRAC_MAX {
			break
		}
		stream.streamID = uint8(PSMUX_EXTENDED_STREAM)
		stream.streamIdExt = info.id_dirac
		info.id_dirac++
		stream.isVideoStream = true
		break
	default:
		fmt.Printf("Stream type 0x%0x not yet implemented", stream_type);
		break
	}

	if stream.streamID == 0 {
		fmt.Printf("Number of elementary streams of type %04x exceeds maximum",
			stream.streamType)
		return nil
	}

	/* XXX: Are private streams also using stream_id_ext? */
	if stream.streamID == uint8(PSMUX_EXTENDED_STREAM) {
		stream.pi.flags |= PSMUX_PACKET_FLAG_PES_EXT_STREAMID
	}

	/* Are these useful at all? */
	if stream.streamID == uint8(PSMUX_PROGRAM_STREAM_MAP) ||
		stream.streamID == uint8(PSMUX_PADDING_STREAM) ||
		stream.streamID == uint8(PSMUX_PRIVATE_STREAM_2) ||
		stream.streamID == uint8(PSMUX_ECM) ||
		stream.streamID == uint8(PSMUX_EMM) ||
		stream.streamID == uint8(PSMUX_PROGRAM_STREAM_DIRECTORY) ||
		stream.streamID == uint8(PSMUX_DSMCC_STREAM) ||
		stream.streamID == uint8(PSMUX_ITU_T_H222_1_TYPE_E) {
		stream.pi.flags &= ^PSMUX_PACKET_FLAG_PES_FULL_HEADER
	} else {
		stream.pi.flags |= PSMUX_PACKET_FLAG_PES_FULL_HEADER
	}

	//stream.cur_buffer = NULL;
	//stream.cur_buffer_consumed = 0;

	stream.curPesPayloadSize = 0;

	//stream.buffer_release = NULL;

	stream.pts = INVALID_TS;
	stream.dts = INVALID_TS;
	stream.lastPts = INVALID_TS;

	/* These fields are set by gstreamer */
	stream.audioSampling = 0;
	stream.audioChannels = 0;
	stream.audioBitrate = 0;
	if stream.maxBufferSize == 0 {
		/* XXX: VLC'S VALUE. Better default? */
		if stream.isVideoStream {
			stream.maxBufferSize = 400 * 1024
		} else if stream.isAudioStream {
			stream.maxBufferSize = 4 * 1024
		} else { /* Unknown */
			stream.maxBufferSize = 4 * 1024
		}
	}

	return stream;
}

func (stream *PsMuxSlicesStream) psmuxStreamPesPayload(pes_hdr_length uint8, payloadLength int, isFistPes, hasRTPHeader *bool, toPackageframeSlices []interfaces.FrameSlice, pOutBuf []byte, pOutSize *int, maxOutSize *int) ([]byte, []interfaces.FrameSlice, error) {
	var outFrameSlice []interfaces.FrameSlice
	stream.curPesPayloadSize = uint16(payloadLength)
	if *maxOutSize < int(pes_hdr_length)+rtp.RTP_HEADER_LEN+4 {
		return nil, nil, fmt.Errorf("OutBuf space is not enough")
	}
	//pes header
	out := pOutBuf
	pOutBuf = pOutBuf[rtp.RTP_HEADER_LEN:]
	*maxOutSize -= int(rtp.RTP_HEADER_LEN)
	*pOutSize += int(rtp.RTP_HEADER_LEN)
	stream.psmuxStreamWritePesHeader(pOutBuf)
	*maxOutSize -= int(pes_hdr_length)
	*pOutSize += int(pes_hdr_length)
	pOutBuf = pOutBuf[pes_hdr_length:]
	firstPkgEnd := rtp.RTP_HEADER_LEN + int(pes_hdr_length)
	if *isFistPes {
		*isFistPes = false
		copy(pOutBuf, []byte{0, 0, 0, 1})
		*maxOutSize -= 4
		*pOutSize += 4
		firstPkgEnd += 4
		pOutBuf = pOutBuf[4:]
	}
	//frame,pes头
	if !*hasRTPHeader { //pes头单独一个rtp包,若帧中前几个slices没有预留rtp头的空间，则将其拷贝到pes，和pes一起打包成一个rtp包
		in := toPackageframeSlices
		index := 0
		v := interfaces.FrameSlice{}
		for index, v = range in {
			if !*hasRTPHeader && v.RtpData != nil && len(v.RtpData) > 0 { //后续的slices不需要拷贝到pes头中
				*hasRTPHeader = true
				toPackageframeSlices = toPackageframeSlices[index:]
				break
			}
			if !*hasRTPHeader { //拷贝到pes头中
				if *maxOutSize < len(v.Data) {
					return nil, nil, fmt.Errorf("OutBuf space is not enough")
				}
				copy(out[firstPkgEnd:], v.Data)
				firstPkgEnd += len(v.Data)
				*maxOutSize -= len(v.Data)
				*pOutSize += len(v.Data)
				pOutBuf = pOutBuf[len(v.Data):]
				continue
			}
		}
		if index == len(in)-1 { //payload已全部拷贝到pes头中
			toPackageframeSlices = nil
		}
	}
	outFrameSlice = append(outFrameSlice, interfaces.FrameSlice{Data: out[rtp.RTP_HEADER_LEN:firstPkgEnd], RtpData: out[:firstPkgEnd]})
	//pes payload
	outFrameSlice = append(outFrameSlice, toPackageframeSlices...)

	return pOutBuf, outFrameSlice, nil
}

func (stream *PsMuxSlicesStream) psmuxStreamMuxFrame(
	frameSlices *interfaces.Packet, pts, dts int64,
	pOutBuf []byte, pOutSize *int, maxOutSize int) ([]interfaces.FrameSlice, error) {
	if frameSlices == nil || maxOutSize < PSMUX_PES_MAX_HDR_LEN {
		return nil, fmt.Errorf("bad parameter")
	}
	var outFrameSlice []interfaces.FrameSlice
	stream.pts = pts
	stream.dts = dts

	*pOutSize = 0

	/* clear pts/dts flag */
	stream.pi.flags &= ^(PSMUX_PACKET_FLAG_PES_WRITE_PTS_DTS |
		PSMUX_PACKET_FLAG_PES_WRITE_PTS)
	/* update pts/dts flag */
	if stream.pts != int64(INVALID_TS) && stream.dts != int64(INVALID_TS) {
		stream.pi.flags |= PSMUX_PACKET_FLAG_PES_WRITE_PTS_DTS;
	} else {
		if stream.pts != int64(INVALID_TS) {
			stream.pi.flags |= PSMUX_PACKET_FLAG_PES_WRITE_PTS;
		}
	}

	pes_hdr_length := stream.psmuxStreamPesHeaderLength()

	length := 4
	isFistPes := true
	hasRTPHeader := false
	var toPackageFrameSlices []interfaces.FrameSlice
	for _, frameSlice := range frameSlices.Data {
		length += len(frameSlice.Data)
		if length > PSMUX_PES_MAX_PAYLOAD {
			var err error
			var outSlice []interfaces.FrameSlice
			if pOutBuf, outSlice, err = stream.psmuxStreamPesPayload(pes_hdr_length, length-len(frameSlice.Data), &isFistPes, &hasRTPHeader, toPackageFrameSlices, pOutBuf, pOutSize, &maxOutSize); err != nil {
				return nil, err
			} else {
				outFrameSlice = append(outFrameSlice, outSlice...)
			}
			length = len(frameSlice.Data)
			toPackageFrameSlices = []interfaces.FrameSlice{}
		}
		toPackageFrameSlices = append(toPackageFrameSlices, frameSlice)
	}

	if len(toPackageFrameSlices) > 0 {
		var err error
		var outSlice []interfaces.FrameSlice
		if pOutBuf, outSlice, err = stream.psmuxStreamPesPayload(pes_hdr_length, length, &isFistPes, &hasRTPHeader, toPackageFrameSlices, pOutBuf, pOutSize, &maxOutSize); err != nil {
			return nil, err
		} else {
			outFrameSlice = append(outFrameSlice, outSlice...)
		}
	}

	return outFrameSlice, nil
}

func (stream *PsMuxSlicesStream) psmuxStreamPesHeaderLength() uint8 {
	/* Calculate the length of the header for this stream */

	/* start_code prefix + stream_id + pes_packet_length = 6 bytes */
	packetLen := 6
	if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_FULL_HEADER) != 0 {
		/* For a PES 'full header' we have at least 3 more bytes,
		 * and then more based on flags */
		packetLen += 3
		if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_WRITE_PTS_DTS) != 0 {
			packetLen += 10
		} else if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_WRITE_PTS) != 0 {
			packetLen += 5
		}
		if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_EXT_STREAMID) != 0 {
			/* Need basic extension flags (1 byte), plus 2 more bytes for the
			 * length + extended stream id */
			packetLen += 3
		}
	}

	return uint8(packetLen)
}

func (stream *PsMuxSlicesStream) psmuxStreamWritePesHeader(data []byte) {
	hdrLen := stream.psmuxStreamPesHeaderLength()

	/* start_code prefix + stream_id + pes_packet_length = 6 bytes */
	data[0] = 0x00
	data[1] = 0x00
	data[2] = 0x01
	data[3] = byte(stream.streamID)
	data = data[4:]

	lengthToWrite := uint16(hdrLen) - 6 + stream.curPesPayloadSize
	data = psmux_put16(data, lengthToWrite);
	if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_FULL_HEADER) != 0 {
		/* Not scrambled, original, not-copyrighted, data_alignment specified by flag */
		flags := uint8(0x88)
		if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_DATA_ALIGN) != 0 {
			flags |= 0x04; /* Enable data_alignment_indicator */
		}
		data[0] = flags
		data = data[1:]

		/* Flags */
		flags = 0;
		if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_WRITE_PTS_DTS) != 0 {
			flags |= 0xC0
		} else if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_WRITE_PTS) != 0 {
			flags |= 0x80;
		}
		if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_EXT_STREAMID) != 0 {
			flags |= 0x01; /* Enable PES_extension_flag */
		}
		data[0] = flags
		data = data[1:]

		/* Header length is the total pes length,
		 * minus the 9 bytes of start codes, flags + hdr_len */
		if hdrLen < 9 {
			return
		}
		data[0] = hdrLen - 9
		data = data[1:]

		if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_WRITE_PTS_DTS) != 0 {
			data = psmux_put_ts(data, 0x3, stream.pts);
			data = psmux_put_ts(data, 0x1, stream.dts);
		} else if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_WRITE_PTS) != 0 {
			data = psmux_put_ts(data, 0x2, stream.pts);
		}

		if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_EXT_STREAMID) != 0 {
			flags = 0x0f; /* preceeding flags all 0 | (reserved bits) | PES_extension_flag_2 */
			data[0] = flags
			extLen := uint8(1);                  /* Only writing 1 byte into the extended fields */
			data[1] = 0x80 | extLen;             /* marker | PES_extension_field_length */
			data[2] = 0x80 | stream.streamIdExt; /* stream_id_extension_flag | extended_stream_id */
			data = data[3:]
		}
	}
}

func (stream *PsMuxSlicesStream) psMuxStreamGetEsDescrs(buf []byte, len *int) {
	if buf == nil || len == nil {
		if len != nil {
			*len = 0
		}
		return
	}
	*len = 0
	/* Based on the stream type, write out any descriptors to go in the
	 * PMT ES_info field */
	pos := buf

	/* tag (registration_descriptor), length, format_identifier */
	switch stream.streamType {
	case PSMUX_ST_AUDIO_AAC:
		/* FIXME */
		break
	case PSMUX_ST_VIDEO_MPEG4:
		/* FIXME */
		break
	case PSMUX_ST_VIDEO_H264:
		pos[0] = 0x05
		pos[1] = 8
		pos[2] = 0x48 /* 'H' */
		pos[3] = 0x44 /* 'D' */
		pos[4] = 0x4D /* 'M' */
		pos[5] = 0x56 /* 'V' */
		/* FIXME : Not sure about this additional_identification_info */
		pos[6] = 0xFF
		pos[7] = 0x1B
		pos[8] = 0x44
		pos[9] = 0x3F
		pos = pos[10:]
		*len += 10
		break
	case PSMUX_ST_VIDEO_DIRAC:
		pos[0] = 0x05
		pos[1] = 4
		pos[2] = 0x64 /* 'd' */
		pos[3] = 0x72 /* 'r' */
		pos[4] = 0x61 /* 'a' */
		pos[5] = 0x63 /* 'c' */
		pos = pos[6:]
		*len += 6
		break
	case PSMUX_ST_PS_AUDIO_AC3:
		{
			pos[0] = 0x05
			pos[1] = 4;
			pos[2] = 0x41 /* 'A' */
			pos[3] = 0x43 /* 'C' */
			pos[4] = 0x2D /* '-' */
			pos[5] = 0x33 /* '3' */

			/* audio_stream_descriptor () | ATSC A/52-2001 Annex A
			 *
			 * descriptor_tag       8 uimsbf
			 * descriptor_length    8 uimsbf
			 * sample_rate_code     3 bslbf
			 * bsid                 5 bslbf
			 * bit_rate_code        6 bslbf
			 * surround_mode        2 bslbf
			 * bsmod                3 bslbf
			 * num_channels         4 bslbf
			 * full_svc             1 bslbf
			 * langcod              8 bslbf
			 * [...]
			 */
			pos[6] = 0x81
			pos[7] = 0x04
			pos = pos[8:]
			*len += 8
			/* 3 bits sample_rate_code, 5 bits hardcoded bsid (default ver 8) */
			switch stream.audioSampling {
			case 48000:
				pos[0] = 0x08
				pos = pos[1:]
				*len += 1
				break
			case 44100:
				pos[0] = 0x28
				pos = pos[1:]
				*len += 1
				break
			case 32000:
				pos[0] = 0x48
				pos = pos[1:]
				*len += 1
				break
			default:
				pos[0] = 0xE8
				pos = pos[1:]
				*len += 1
				break /* 48, 44.1 or 32 Khz */
			}

			/* 1 bit bit_rate_limit, 5 bits bit_rate_code, 2 bits suround_mode */
			switch stream.audioBitrate {
			case 32:
				pos[0] = 0x00 << 2
				pos = pos[1:]
				*len += 1
				break
			case 40:
				pos[0] = 0x01 << 2
				pos = pos[1:]
				*len += 1
				break
			case 48:
				pos[0] = 0x02 << 2
				pos = pos[1:]
				*len += 1
				break
			case 56:
				pos[0] = 0x03 << 2
				pos = pos[1:]
				*len += 1
				break
			case 64:
				pos[0] = 0x04 << 2
				pos = pos[1:]
				*len += 1
				break
			case 80:
				pos[0] = 0x05 << 2
				pos = pos[1:]
				*len += 1
				break
			case 96:
				pos[0] = 0x06 << 2
				pos = pos[1:]
				*len += 1
				break
			case 112:
				pos[0] = 0x07 << 2
				pos = pos[1:]
				*len += 1
				break
			case 128:
				pos[0] = 0x08 << 2
				pos = pos[1:]
				*len += 1
				break
			case 160:
				pos[0] = 0x09 << 2
				pos = pos[1:]
				*len += 1
				break
			case 192:
				pos[0] = 0x0A << 2
				pos = pos[1:]
				*len += 1
				break
			case 224:
				pos[0] = 0x0B << 2
				pos = pos[1:]
				*len += 1
				break
			case 256:
				pos[0] = 0x0C << 2
				pos = pos[1:]
				*len += 1
				break
			case 320:
				pos[0] = 0x0D << 2
				pos = pos[1:]
				*len += 1
				break
			case 384:
				pos[0] = 0x0E << 2
				pos = pos[1:]
				*len += 1
				break
			case 448:
				pos[0] = 0x0F << 2
				pos = pos[1:]
				*len += 1
				break
			case 512:
				pos[0] = 0x10 << 2
				pos = pos[1:]
				*len += 1
				break
			case 576:
				pos[0] = 0x11 << 2
				pos = pos[1:]
				*len += 1
				break
			case 640:
				pos[0] = 0x12 << 2
				pos = pos[1:]
				*len += 1
				break
			default:
				pos[0] = 0x32 << 2
				pos = pos[1:]
				*len += 1
				break /* 640 Kb/s upper limit */
			}

			/* 3 bits bsmod, 4 bits num_channels, 1 bit full_svc */
			switch stream.audioChannels {
			case 1:
				pos[0] = 0x01 << 1
				pos = pos[1:]
				*len += 1
				break /* 1/0 */
			case 2:
				pos[0] = 0x02 << 1
				pos = pos[1:]
				*len += 1
				break /* 2/0 */
			case 3:
				pos[0] = 0x0A << 1
				pos = pos[1:]
				*len += 1
				break /* <= 3 */
			case 4:
				pos[0] = 0x0B << 1
				pos = pos[1:]
				*len += 1
				break /* <= 4 */
			case 5:
				pos[0] = 0x0C << 1
				pos = pos[1:]
				*len += 1
				break /* <= 5 */
			case 6:
			default:
				pos[0] = 0x0D << 1
				pos = pos[1:]
				*len += 1
				break /* <= 6 */
			}

			pos[0] = 0x00
			pos = pos[1:]
			*len += 1
			break
		}
	case PSMUX_ST_PS_AUDIO_DTS:
		/* FIXME */
		break
	case PSMUX_ST_PS_AUDIO_LPCM:
		/* FIXME */
		break
	default:
		break
	}
}

func (stream *PsMuxSlicesStream) psMuxStreamGetPts() int64 {
	if stream == nil {
		return -1
	}
	return stream.lastPts
}
