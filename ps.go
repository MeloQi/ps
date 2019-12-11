package ps

import "fmt"

const (
	BUF_LEN int = (1024 * 1024)
)

type H264OnlyPsMux struct {
	pMuxOutBuf   []byte
	gb28181PsMux *Gb28181PsMux
	idx          int
}

func NewH264OnlyPsMux() *H264OnlyPsMux {
	m := &H264OnlyPsMux{pMuxOutBuf: make([]byte, BUF_LEN), gb28181PsMux: NewGb28181PsMux()}
	m.idx = m.gb28181PsMux.AddStream(PSMUX_ST_VIDEO_H264)
	return m
}

func (m *H264OnlyPsMux) Mux(data []byte, pts int64) ([]byte, error) {
	pMuxOutSize := 0
	if err := m.gb28181PsMux.MuxH264SingleFrame(data, len(data), pts, pts, m.idx, m.pMuxOutBuf, &pMuxOutSize, BUF_LEN); err != nil {
		return nil, err
	}
	if pMuxOutSize == 0 {
		return nil, nil
	}
	return m.pMuxOutBuf[:pMuxOutSize], nil
}

//Gb28181PsMux class
type Gb28181PsMux struct {
	m_PsMuxContext   *PsMux
	m_VecStream      []*PsMuxStream
	m_SpsPpsIBuf     []byte
	m_SpsPpsIBufSize int
}

func NewGb28181PsMux() *Gb28181PsMux {
	return &Gb28181PsMux{m_PsMuxContext: NewPsMux(),
		m_VecStream:      []*PsMuxStream{},
		m_SpsPpsIBuf:     make([]byte, MAX_SPSPPSI_SIZE),
		m_SpsPpsIBufSize: 0}
}

func (this *Gb28181PsMux) AddStream(Type uint8) int {
	if this.m_PsMuxContext == nil {
		this.m_PsMuxContext = NewPsMux()
	}
	pStream := this.m_PsMuxContext.psmux_create_stream(Type)
	this.m_VecStream = append(this.m_VecStream, pStream)
	return (len(this.m_VecStream) - 1)
}

//输入单个H264/5帧,必须以00 00 00 01或者00 00 01开头,SPS PPS 和 I帧不能连在一起
func (this *Gb28181PsMux) MuxH264SingleFrame(buf []byte, length int, Pts, Dts int64, Idx int,
	outBuf []byte, pOutSize *int, maxOutSize int) error {
	*pOutSize = 0
	if Idx >= len(this.m_VecStream) {
		return fmt.Errorf("Idx >= m_VecStream.size()")
	}
	if length < 6 {
		return fmt.Errorf("MuxH264SingleFrame incomming data length < 6")
	}

	c := uint8(0)
	if !isH264Or265Frame(buf, &c) {
		return fmt.Errorf("isH264Or265Frame is false")
	}

	Type := getH264NALtype(c)
	if Type == NAL_other {
		return fmt.Errorf("NAL Type Err")
	}
	if Type == NAL_SEI {
		return nil
	}

	pMuxStream := this.m_VecStream[Idx]

	//default
	this.m_PsMuxContext.enable_pack_hdr = false
	this.m_PsMuxContext.enable_psm = false
	this.m_PsMuxContext.enable_sys_hdr = false
	pMuxStream.pi.flags &= ^PSMUX_PACKET_FLAG_PES_DATA_ALIGN
	this.m_PsMuxContext.pts = Pts

	if Pts == Dts {
		Dts = INVALID_TS
	}

	if Type == NAL_PFRAME {
		this.m_PsMuxContext.enable_pack_hdr = true
		pMuxStream.pi.flags |= PSMUX_PACKET_FLAG_PES_DATA_ALIGN
		if err := this.m_PsMuxContext.psmux_mux_frame(this.m_VecStream[Idx], buf, length, Pts, Dts, outBuf, pOutSize, maxOutSize); err != nil {
			return err
		}
	} else {
		//如果是单个SPS PPS 则等到I帧一起发送,原则就是同一个时间戳作为一个RTP包
		if Type == NAL_SPS {
			this.m_PsMuxContext.enable_pack_hdr = true
			this.m_PsMuxContext.enable_psm = true
			this.m_PsMuxContext.enable_sys_hdr = true
			Pts = INVALID_TS
			Dts = INVALID_TS
		} else if Type == NAL_PPS {
			Pts = INVALID_TS
			Dts = INVALID_TS
		} else if Type == NAL_IDR {
			pMuxStream.pi.flags |= PSMUX_PACKET_FLAG_PES_DATA_ALIGN
		}

		outSize := 0
		tmpOutBuf := this.m_SpsPpsIBuf[this.m_SpsPpsIBufSize:]
		this.m_PsMuxContext.psmux_mux_frame(this.m_VecStream[Idx], buf, length, Pts, Dts, tmpOutBuf, &outSize, MAX_SPSPPSI_SIZE-this.m_SpsPpsIBufSize)
		this.m_SpsPpsIBufSize += outSize
		if Type == NAL_IDR {
			if this.m_SpsPpsIBufSize > maxOutSize {
				return fmt.Errorf("this.m_PsMuxContext.m_SpsPpsIBufSize > maxOutSize")
			}
			src := this.m_SpsPpsIBuf[:this.m_SpsPpsIBufSize]
			copy(outBuf, src)
			*pOutSize = this.m_SpsPpsIBufSize
			this.m_SpsPpsIBufSize = 0
		} else {
			return nil
		}

	}

	return nil
}

func isH264Or265Frame(data []byte, NalTypeChar *uint8) bool {
	bOk := false
	if data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1 {
		if NalTypeChar != nil {
			*NalTypeChar = data[4];
		}
		bOk = true
	}

	if data[0] == 0 && data[1] == 0 && data[2] == 1 {
		if NalTypeChar != nil {
			*NalTypeChar = data[3]
		}
		bOk = true
	}
	return bOk
}

func getH264NALtype(c uint8) uint8 {
	switch (c & 0x1f) {
	case 6:
		return NAL_SEI
	case 7:
		return NAL_SPS
	case 8:
		return NAL_PPS
	case 5:
		return NAL_IDR
	case 1:
		return NAL_PFRAME
	default:
		return NAL_other
	}
	return NAL_other
}

//bitsBuffer bits buffer
type bitsBuffer struct {
	iSize int
	iData int
	iMask uint8
	pData []byte
}

func bitsInit(isize int, buffer []byte) *bitsBuffer {

	bits := &bitsBuffer{
		iSize: isize,
		iData: 0,
		iMask: 0x80,
		pData: buffer,
	}
	if bits.pData == nil {
		bits.pData = make([]byte, isize)
	}
	return bits
}

func bitsWrite(bits *bitsBuffer, count int, src uint64) *bitsBuffer {

	for count > 0 {
		count--
		if ((src >> uint(count)) & 0x01) != 0 {
			bits.pData[bits.iData] |= bits.iMask
		} else {
			bits.pData[bits.iData] &= ^bits.iMask
		}
		bits.iMask >>= 1
		if bits.iMask == 0 {
			bits.iData++
			bits.iMask = 0x80
		}
	}

	return bits
}

func psmux_put16(pos []byte, val uint16) []byte {
	pos[0] = byte((val >> 8) & 0xff)
	pos[1] = byte(val & 0xff)
	return pos[2:]
}

func psmux_put32(pos []byte, val uint32) []byte {
	pos[0] = byte((val >> 24) & 0xff)
	pos[1] = byte(val >> 16 & 0xff)
	pos[2] = byte((val >> 8) & 0xff)
	pos[3] = byte(val & 0xff)
	return pos[4:]
}

func psmux_put_ts(pos []byte, id uint8, ts int64) []byte {
	/* 1: 4 bit id value | TS [32..30] | marker_bit */
	pos[0] = ((id << 4) | (uint8(ts>>29) & 0x0E) | 0x01) & 0xff
	pos = pos[1:]
	/* 2, 3: TS[29..15] | marker_bit */
	pos = psmux_put16(pos, uint16(((ts>>14)&0xfffe)|0x01))
	/* 4, 5: TS[14..0] | marker_bit */
	pos = psmux_put16(pos, uint16(((ts<<1)&0xfffe)|0x01))
	return pos
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

const (
	BIT_RATE uint64 = 400 * 1024

	MAX_SPSPPSI_SIZE int = (1024 * 1024)

	PSMUX_PES_MAX_PAYLOAD    int = 65500
	PSMUX_PES_MAX_HDR_LEN    int = 30
	PSMUX_MAX_PACKET_LEN     int = PSMUX_PES_MAX_PAYLOAD + PSMUX_PES_MAX_HDR_LEN
	PSMUX_MAX_ES_INFO_LENGTH int = ((1 << 12) - 1)
	MAX_MUX_STREAM_NUM       int = (2)

	PSMUX_START_CODE_PREFIX uint64 = 0x01

	/* stream_id */
	PSMUX_PACK_HEADER              uint64 = 0xba
	PSMUX_SYSTEM_HEADER            uint64 = 0xbb
	PSMUX_PROGRAM_STREAM_MAP       uint64 = 0xbc
	PSMUX_PRIVATE_STREAM_1         uint64 = 0xbd
	PSMUX_PADDING_STREAM           uint64 = 0xbe
	PSMUX_PRIVATE_STREAM_2         uint64 = 0xbf
	PSMUX_ECM                      uint64 = 0xb0
	PSMUX_EMM                      uint64 = 0xb1
	PSMUX_PROGRAM_STREAM_DIRECTORY uint64 = 0xff
	PSMUX_DSMCC_STREAM             uint64 = 0xf2
	PSMUX_ITU_T_H222_1_TYPE_E      uint64 = 0xf8
	PSMUX_EXTENDED_STREAM          uint64 = 0xfd
	PSMUX_PROGRAM_END              uint64 = 0xb9

	/*StreamType*/
	/*Table 2-29 in spec */
	PSMUX_ST_RESERVED         uint8 = 0x00
	PSMUX_ST_VIDEO_MPEG1      uint8 = 0x01
	PSMUX_ST_VIDEO_MPEG2      uint8 = 0x02
	PSMUX_ST_AUDIO_MPEG1      uint8 = 0x03
	PSMUX_ST_AUDIO_MPEG2      uint8 = 0x04
	PSMUX_ST_PRIVATE_SECTIONS uint8 = 0x05
	PSMUX_ST_PRIVATE_DATA     uint8 = 0x06
	PSMUX_ST_MHEG             uint8 = 0x07
	PSMUX_ST_DSMCC            uint8 = 0x08
	PSMUX_ST_H222_1           uint8 = 0x09
	/* later extensions */
	PSMUX_ST_AUDIO_AAC   uint8 = 0x0f
	PSMUX_ST_VIDEO_MPEG4 uint8 = 0x10
	PSMUX_ST_VIDEO_H264  uint8 = 0x1b
	/* private stream types */
	PSMUX_ST_PS_AUDIO_AC3      uint8 = 0x81
	PSMUX_ST_PS_AUDIO_DTS      uint8 = 0x8a
	PSMUX_ST_PS_AUDIO_LPCM     uint8 = 0x8b
	PSMUX_ST_PS_AUDIO_G711A    uint8 = 0x90
	PSMUX_ST_PS_AUDIO_G711U    uint8 = 0x91
	PSMUX_ST_PS_AUDIO_G722_1   uint8 = 0x92
	PSMUX_ST_PS_AUDIO_G723_1   uint8 = 0x93
	PSMUX_ST_PS_AUDIO_G729     uint8 = 0x99
	PSMUX_ST_PS_AUDIO_SVAC     uint8 = 0x9b
	PSMUX_ST_PS_DVD_SUBPICTURE uint8 = 0xff
	/* Non-standard definitions */
	PSMUX_ST_VIDEO_DIRAC uint8 = 0xD1

	/* TODO: flags? looks that we don't need these */
	PSMUX_PACKET_FLAG_NONE            uint32 = (0)
	PSMUX_PACKET_FLAG_ADAPTATION      uint32 = (1 << 0)
	PSMUX_PACKET_FLAG_DISCONT         uint32 = (1 << 1)
	PSMUX_PACKET_FLAG_RANDOM_ACCESS   uint32 = (1 << 2)
	PSMUX_PACKET_FLAG_PRIORITY        uint32 = (1 << 3)
	PSMUX_PACKET_FLAG_WRITE_PCR       uint32 = (1 << 4)
	PSMUX_PACKET_FLAG_WRITE_OPCR      uint32 = (1 << 5)
	PSMUX_PACKET_FLAG_WRITE_SPLICE    uint32 = (1 << 6)
	PSMUX_PACKET_FLAG_WRITE_ADAPT_EXT uint32 = (1 << 7)

	/* PES stream specific flags */
	PSMUX_PACKET_FLAG_PES_FULL_HEADER   uint32 = (1 << 8)
	PSMUX_PACKET_FLAG_PES_WRITE_PTS     uint32 = (1 << 9)
	PSMUX_PACKET_FLAG_PES_WRITE_PTS_DTS uint32 = (1 << 10)
	PSMUX_PACKET_FLAG_PES_WRITE_ESCR    uint32 = (1 << 11)
	PSMUX_PACKET_FLAG_PES_EXT_STREAMID  uint32 = (1 << 12)
	PSMUX_PACKET_FLAG_PES_DATA_ALIGN    uint32 = (1 << 13)

	INVALID_TS int64 = (-1)

	/* NAL_type */
	NAL_IDR        uint8 = 0
	NAL_SPS        uint8 = 1
	NAL_PPS        uint8 = 2
	NAL_SEI        uint8 = 3
	NAL_PFRAME     uint8 = 4
	NAL_VPS        uint8 = 5
	NAL_SEI_PREFIX uint8 = 6
	NAL_SEI_SUFFIX uint8 = 7
	NAL_other      uint8 = 8
	NAL_TYPE_NUM   uint8 = 9

	CLOCKBASE                   float64 = 90000
	PSMUX_PACK_HDR_INTERVAL     float64 = (0.7 * CLOCKBASE) /* interval to update pack header. 0.7 sec */
	PSMUX_BITRATE_CALC_INTERVAL float64 = CLOCKBASE         /* interval to update bitrate in pack header. 1 sec */

	/* stream_id assignemnt */
	PSMUX_STREAM_ID_MPGA_INIT  uint8 = 0xc0
	PSMUX_STREAM_ID_MPGA_MAX   uint8 = 0xcf
	PSMUX_STREAM_ID_MPGV_INIT  uint8 = 0xe0
	PSMUX_STREAM_ID_MPGV_MAX   uint8 = 0xef
	PSMUX_STREAM_ID_AC3_INIT   uint8 = 0x80
	PSMUX_STREAM_ID_AC3_MAX    uint8 = 0x87
	PSMUX_STREAM_ID_SPU_INIT   uint8 = 0x20
	PSMUX_STREAM_ID_SPU_MAX    uint8 = 0x3f
	PSMUX_STREAM_ID_DTS_INIT   uint8 = 0x88
	PSMUX_STREAM_ID_DTS_MAX    uint8 = 0x8f
	PSMUX_STREAM_ID_LPCM_INIT  uint8 = 0xa0
	PSMUX_STREAM_ID_LPCM_MAX   uint8 = 0xaf
	PSMUX_STREAM_ID_DIRAC_INIT uint8 = 0x60
	PSMUX_STREAM_ID_DIRAC_MAX  uint8 = 0x6f
)

//PsMux class
type PsMux struct {
	streams []*PsMuxStream
	id_info PsMuxStreamIdInfo

	pts     int64
	bitRate uint64
	/* bounds in system header */
	audioBound uint8
	videoBound uint8
	rateBound  uint64
	packetBuf  []byte
	esInfoBuf  []byte

	enable_pack_hdr bool
	pack_hdr_pts    int64
	enable_sys_hdr  bool
	sys_hdr_pts     int64
	enable_psm      bool
	psm_pts         int64
	pes_cnt         uint32
	bit_size        uint64 /* accumulated bit size of processed data */
	bit_pts         int64  /* last time the bit_rate is updated */
}

type PsMuxStreamIdInfo struct {
	id_mpga  uint8
	id_mpgv  uint8
	id_ac3   uint8
	id_spu   uint8
	id_dts   uint8
	id_lpcm  uint8
	id_dirac uint8
}

func NewPsMux() *PsMux {
	return &PsMux{packetBuf: make([]byte, PSMUX_MAX_PACKET_LEN),
		esInfoBuf:    make([]byte, PSMUX_MAX_ES_INFO_LENGTH),
		streams:      []*PsMuxStream{},
		pts:          INVALID_TS,
		pack_hdr_pts: INVALID_TS,
		sys_hdr_pts:  INVALID_TS,
		psm_pts:      INVALID_TS,
		bit_pts:      0,
		bitRate:      BIT_RATE,
		rateBound:    (2 * BIT_RATE) / 400,
		videoBound:   0,
		audioBound:   0,
		id_info: PsMuxStreamIdInfo{id_mpga: PSMUX_STREAM_ID_MPGA_INIT,
			id_mpgv:  PSMUX_STREAM_ID_MPGV_INIT,
			id_ac3:   PSMUX_STREAM_ID_AC3_INIT,
			id_spu:   PSMUX_STREAM_ID_SPU_INIT,
			id_dts:   PSMUX_STREAM_ID_DTS_INIT,
			id_lpcm:  PSMUX_STREAM_ID_LPCM_INIT,
			id_dirac: PSMUX_STREAM_ID_DIRAC_INIT}}
}

func (mux *PsMux) psmux_create_stream(stream_type uint8) *PsMuxStream {
	if len(mux.streams) >= MAX_MUX_STREAM_NUM {
		return nil
	}

	stream := NewPsmuxStream(mux, stream_type)
	mux.streams = append(mux.streams, stream)

	if stream.isVideoStream {
		mux.videoBound++;
		if mux.videoBound > 32 {
			fmt.Printf("Number of video es exceeds upper limit")
		}
	} else if (stream.isAudioStream) {
		mux.audioBound++;
		if (mux.audioBound > 64) {
			fmt.Printf("Number of audio es exceeds upper limit")
		}
	}

	return stream;
}

func (mux *PsMux) psmux_mux_frame(stream *PsMuxStream, rawBuf []byte, rawBuflen int, pts, dts int64,
	pOutBuf []byte, pOutSize *int, maxOutSize int) error {

	if stream == nil || rawBuf == nil || rawBuflen == 0 || pOutBuf == nil || maxOutSize == 0 {
		return fmt.Errorf("bad parameter")
	}

	{
		ts := stream.psmux_stream_get_pts();
		if ts != INVALID_TS {
			mux.pts = ts;
		}
	}

	*pOutSize = 0

	if mux.enable_pack_hdr {
		if (mux.pts != INVALID_TS && mux.pts > mux.bit_pts && mux.pts-mux.bit_pts > int64(PSMUX_BITRATE_CALC_INTERVAL)) {
			/* XXX: smoothing the rate? */
			mux.bitRate = uint64((float64(mux.bit_size) * 8 * CLOCKBASE) / float64(mux.pts-mux.bit_pts))
			mux.bit_size = 0
			mux.bit_pts = mux.pts
		}
		muxHdrSize := 0
		if err := mux.psmux_write_pack_header(pOutBuf, &muxHdrSize, maxOutSize); err != nil {
			return err
		}
		maxOutSize -= muxHdrSize
		*pOutSize += muxHdrSize
		pOutBuf = pOutBuf[muxHdrSize:]
		mux.pack_hdr_pts = mux.pts
	}

	if mux.enable_sys_hdr {
		muxSysHdrSize := 0
		if err := mux.psmux_write_system_header(pOutBuf, &muxSysHdrSize, maxOutSize); err != nil {
			return err
		}
		maxOutSize -= muxSysHdrSize
		*pOutSize += muxSysHdrSize
		pOutBuf = pOutBuf[muxSysHdrSize:]
		mux.sys_hdr_pts = mux.pts
	}

	if mux.enable_psm {
		muxPsmSize := 0
		if err := mux.psmux_write_program_stream_map(pOutBuf, &muxPsmSize, maxOutSize); err != nil {
			return err
		}
		maxOutSize -= muxPsmSize
		*pOutSize += muxPsmSize
		pOutBuf = pOutBuf[muxPsmSize:]
		mux.psm_pts = mux.pts
	}

	PesSize := 0
	if err := stream.psmux_stream_mux_frame(rawBuf, rawBuflen, pts, dts, pOutBuf, &PesSize, maxOutSize); err != nil {
		return err
	}
	*pOutSize += PesSize;
	mux.pes_cnt += 1

	return nil
}

func (mux *PsMux) psmux_write_pack_header(outBuf []byte, pOutSize *int, maxOutSize int) error {
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
		if (muxRate > mux.rateBound/2) {
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

func (mux *PsMux) psmux_write_system_header(outBuf []byte, pOutSize *int, maxOutSize int) error {
	len := 12 + len(mux.streams)*3

	/* system_header_start_code */
	bw := bitsInit(len, mux.packetBuf)

	/* system_header start code */
	bitsWrite(bw, 24, PSMUX_START_CODE_PREFIX)
	bitsWrite(bw, 8, PSMUX_SYSTEM_HEADER)
	bitsWrite(bw, 16, uint64(len-6))         /* header_length */
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

	if maxOutSize < len {
		return fmt.Errorf("maxOutSize < %d", len)
	}
	data := bw.pData[:len]
	copy(outBuf, data)
	if pOutSize != nil {
		*pOutSize = len
	}

	return nil
}

func (mux *PsMux) psmux_write_program_stream_map(outBuf []byte, pOutSize *int, maxOutSize int) error {
	psm_size := 16
	es_map_size := 0
	len := 0

	/* pre-write the descriptor loop */
	pos := mux.esInfoBuf
	for _, stream := range mux.streams {
		len = 0
		pos[0] = stream.streamType
		pos[1] = stream.streamID
		pos = pos[2:]
		stream.psmux_stream_get_es_descrs(pos[2:], &len)
		pos = psmux_put16(pos, uint16(len))
		es_map_size += len + 4
		pos = pos[len:]
	}

	psm_size += es_map_size
	bw := bitsInit(psm_size, mux.packetBuf)

	/* psm start code */
	bitsWrite(bw, 24, PSMUX_START_CODE_PREFIX)
	bitsWrite(bw, 8, PSMUX_PROGRAM_STREAM_MAP)
	bitsWrite(bw, 16, uint64(psm_size-6)) /* psm_length */
	bitsWrite(bw, 1, 1)                   /* current_next_indicator */
	bitsWrite(bw, 2, 0xF)                 /* reserved */
	bitsWrite(bw, 5, 0x1)                 /* psm_version = 1 */
	bitsWrite(bw, 7, 0xFF)                /* reserved */
	bitsWrite(bw, 1, 1)                   /* marker */

	bitsWrite(bw, 16, 0) /* program_stream_info_length */
	/* program_stream_info empty */

	bitsWrite(bw, 16, uint64(es_map_size)) /* elementary_stream_map_length */
	dst := bw.pData[bw.iData:]
	src := mux.esInfoBuf[:es_map_size]
	copy(dst, src)

	/* CRC32 */
	{
		crc := calc_crc32(mux.packetBuf[:psm_size-4])
		pos := mux.packetBuf[psm_size-4:]
		pos = psmux_put32(pos, crc);
	}

	if maxOutSize < psm_size {
		return fmt.Errorf("maxOutSize < %d", psm_size)
	}
	data := bw.pData[:psm_size]
	copy(outBuf, data)
	if pOutSize != nil {
		*pOutSize = psm_size
	}

	return nil
}

//PsMuxStream class
type PsMuxStream struct {
	pi PsMuxPacketInfo

	streamType    uint8
	streamID      uint8
	stream_id_ext uint8

	/* PES payload */
	cur_pes_payload_size uint16
	pes_bytes_written    uint16 /* delete*/

	/* stream type */
	isVideoStream bool
	isAudioStream bool

	/* PTS/DTS to write if the flags in the packet info are set */
	pts      int64 /* TODO: cur_buffer->pts?*/
	dts      int64 /* TODO: cur_buffer->dts?*/
	last_pts int64

	/* for writing descriptors */
	audioSampling int
	audioChannels int
	audioBitrate  int

	/* for writing buffer size in system header */
	maxBufferSize uint64
}

type PsMuxPacketInfo struct {
	flags uint32
}

func NewPsmuxStream(mux *PsMux, stream_type uint8) *PsMuxStream {
	info := &(mux.id_info)
	stream := &PsMuxStream{
		streamType:    stream_type,
		isAudioStream: false,
		isVideoStream: false,
		streamID:      0,
		maxBufferSize: 0,
	}

	switch (stream_type) {
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
		stream.stream_id_ext = 0
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
		stream.stream_id_ext = 0
		stream.isVideoStream = true
		break
		/* AC3 / A52 */
	case PSMUX_ST_PS_AUDIO_AC3:
		if info.id_ac3 > PSMUX_STREAM_ID_AC3_MAX {
			break
		}
		stream.streamID = uint8(PSMUX_PRIVATE_STREAM_1)
		stream.stream_id_ext = info.id_ac3
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
		stream.stream_id_ext = info.id_dts
		info.id_dts++
		stream.isAudioStream = true
		break
		/* LPCM */
	case PSMUX_ST_PS_AUDIO_LPCM:
		if info.id_lpcm > PSMUX_STREAM_ID_LPCM_MAX {
			break
		}
		stream.streamID = uint8(PSMUX_PRIVATE_STREAM_1)
		stream.stream_id_ext = info.id_lpcm
		info.id_lpcm++
		stream.isAudioStream = true
		break
	case PSMUX_ST_VIDEO_DIRAC:
		if info.id_dirac > PSMUX_STREAM_ID_DIRAC_MAX {
			break
		}
		stream.streamID = uint8(PSMUX_EXTENDED_STREAM)
		stream.stream_id_ext = info.id_dirac
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
	if (stream.streamID == uint8(PSMUX_PROGRAM_STREAM_MAP) ||
		stream.streamID == uint8(PSMUX_PADDING_STREAM) ||
		stream.streamID == uint8(PSMUX_PRIVATE_STREAM_2) ||
		stream.streamID == uint8(PSMUX_ECM) ||
		stream.streamID == uint8(PSMUX_EMM) ||
		stream.streamID == uint8(PSMUX_PROGRAM_STREAM_DIRECTORY) ||
		stream.streamID == uint8(PSMUX_DSMCC_STREAM) ||
		stream.streamID == uint8(PSMUX_ITU_T_H222_1_TYPE_E)) {
		stream.pi.flags &= ^PSMUX_PACKET_FLAG_PES_FULL_HEADER
	} else {
		stream.pi.flags |= PSMUX_PACKET_FLAG_PES_FULL_HEADER
	}

	//stream.cur_buffer = NULL;
	//stream.cur_buffer_consumed = 0;

	stream.cur_pes_payload_size = 0;

	//stream.buffer_release = NULL;

	stream.pts = INVALID_TS;
	stream.dts = INVALID_TS;
	stream.last_pts = INVALID_TS;

	/* These fields are set by gstreamer */
	stream.audioSampling = 0;
	stream.audioChannels = 0;
	stream.audioBitrate = 0;
	if (stream.maxBufferSize == 0) {
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

func (stream *PsMuxStream) psmux_stream_mux_frame(
	rawBuf []byte, rawBuflen int, pts, dts int64,
	pOutBuf []byte, pOutSize *int, maxOutSize int) error {
	if rawBuf == nil || maxOutSize < PSMUX_PES_MAX_HDR_LEN {
		return fmt.Errorf("bad parameter")
	}

	stream.pts = pts
	stream.dts = dts

	*pOutSize = 0

	/* clear pts/dts flag */
	stream.pi.flags &= ^(PSMUX_PACKET_FLAG_PES_WRITE_PTS_DTS |
		PSMUX_PACKET_FLAG_PES_WRITE_PTS)
	/* update pts/dts flag */
	if (stream.pts != int64(INVALID_TS) && stream.dts != int64(INVALID_TS)) {
		stream.pi.flags |= PSMUX_PACKET_FLAG_PES_WRITE_PTS_DTS;
	} else {
		if (stream.pts != int64(INVALID_TS)) {
			stream.pi.flags |= PSMUX_PACKET_FLAG_PES_WRITE_PTS;
		}
	}

	pes_hdr_length := stream.psmux_stream_pes_header_length()

	len := rawBuflen
	cur := rawBuf

	for len > 0 {
		stream.cur_pes_payload_size =
			uint16(min(int64(len), int64(PSMUX_PES_MAX_PAYLOAD-int(pes_hdr_length))))

		if maxOutSize < int(pes_hdr_length) {
			return fmt.Errorf("OutBuf space is not enough")
		}

		stream.psmux_stream_write_pes_header(pOutBuf)
		maxOutSize -= int(pes_hdr_length)
		pOutBuf = pOutBuf[pes_hdr_length:]
		if (maxOutSize < int(stream.cur_pes_payload_size)) {
			return fmt.Errorf("OutBuf space is not enough")
		}
		src := cur[:stream.cur_pes_payload_size]
		copy(pOutBuf, src)
		cur = cur[stream.cur_pes_payload_size:]
		len -= int(stream.cur_pes_payload_size) /* number of bytes of payload to write */
		pOutBuf = pOutBuf[stream.cur_pes_payload_size:]
		maxOutSize -= int(stream.cur_pes_payload_size)
		*pOutSize += int(int(pes_hdr_length) + int(stream.cur_pes_payload_size))
	}

	return nil
}

func (stream *PsMuxStream) psmux_stream_pes_header_length() uint8 {
	/* Calculate the length of the header for this stream */

	/* start_code prefix + stream_id + pes_packet_length = 6 bytes */
	packet_len := 6
	if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_FULL_HEADER) != 0 {
		/* For a PES 'full header' we have at least 3 more bytes,
		 * and then more based on flags */
		packet_len += 3
		if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_WRITE_PTS_DTS) != 0 {
			packet_len += 10
		} else if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_WRITE_PTS) != 0 {
			packet_len += 5
		}
		if (stream.pi.flags & PSMUX_PACKET_FLAG_PES_EXT_STREAMID) != 0 {
			/* Need basic extension flags (1 byte), plus 2 more bytes for the
			 * length + extended stream id */
			packet_len += 3
		}
	}

	return uint8(packet_len)
}

func (stream *PsMuxStream) psmux_stream_write_pes_header(data []byte) {
	hdr_len := stream.psmux_stream_pes_header_length()

	/* start_code prefix + stream_id + pes_packet_length = 6 bytes */
	data[0] = 0x00
	data[1] = 0x00
	data[2] = 0x01
	data[3] = byte(stream.streamID)
	data = data[4:]

	length_to_write := uint16(hdr_len) - 6 + stream.cur_pes_payload_size
	data = psmux_put16(data, length_to_write);
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
		if hdr_len < 9 {
			return
		}
		data[0] = (hdr_len - 9)
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
			ext_len := uint8(1);                   /* Only writing 1 byte into the extended fields */
			data[1] = 0x80 | ext_len;              /* marker | PES_extension_field_length */
			data[2] = 0x80 | stream.stream_id_ext; /* stream_id_extension_flag | extended_stream_id */
			data = data[3:]
		}
	}
}

func (stream *PsMuxStream) psmux_stream_get_es_descrs(buf []byte, len *int) {
	if buf == nil || len == nil {
		if (len != nil) {
			*len = 0
		}
		return
	}
	*len = 0
	/* Based on the stream type, write out any descriptors to go in the
	 * PMT ES_info field */
	pos := buf

	/* tag (registration_descriptor), length, format_identifier */
	switch (stream.streamType) {
	case PSMUX_ST_AUDIO_AAC:
		/* FIXME */
		break;
	case PSMUX_ST_VIDEO_MPEG4:
		/* FIXME */
		break;
	case PSMUX_ST_VIDEO_H264:
		pos[0] = 0x05;
		pos[1] = 8;
		pos[2] = 0x48; /* 'H' */
		pos[3] = 0x44; /* 'D' */
		pos[4] = 0x4D; /* 'M' */
		pos[5] = 0x56; /* 'V' */
		/* FIXME : Not sure about this additional_identification_info */
		pos[6] = 0xFF;
		pos[7] = 0x1B;
		pos[8] = 0x44;
		pos[9] = 0x3F;
		pos = pos[10:]
		*len += 10
		break;
	case PSMUX_ST_VIDEO_DIRAC:
		pos[0] = 0x05;
		pos[1] = 4;
		pos[2] = 0x64; /* 'd' */
		pos[3] = 0x72; /* 'r' */
		pos[4] = 0x61; /* 'a' */
		pos[5] = 0x63; /* 'c' */
		pos = pos[6:]
		*len += 6
		break;
	case PSMUX_ST_PS_AUDIO_AC3:
		{
			pos[0] = 0x05;
			pos[1] = 4;
			pos[2] = 0x41; /* 'A' */
			pos[3] = 0x43; /* 'C' */
			pos[4] = 0x2D; /* '-' */
			pos[5] = 0x33; /* '3' */

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
			pos[6] = 0x81;
			pos[7] = 0x04;
			pos = pos[8:]
			*len += 8
			/* 3 bits sample_rate_code, 5 bits hardcoded bsid (default ver 8) */
			switch (stream.audioSampling) {
			case 48000:
				pos[0] = 0x08;
				pos = pos[1:]
				*len += 1
				break;
			case 44100:
				pos[0] = 0x28;
				pos = pos[1:]
				*len += 1
				break;
			case 32000:
				pos[0] = 0x48;
				pos = pos[1:]
				*len += 1
				break;
			default:
				pos[0] = 0xE8;
				pos = pos[1:]
				*len += 1
				break; /* 48, 44.1 or 32 Khz */
			}

			/* 1 bit bit_rate_limit, 5 bits bit_rate_code, 2 bits suround_mode */
			switch (stream.audioBitrate) {
			case 32:
				pos[0] = 0x00 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 40:
				pos[0] = 0x01 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 48:
				pos[0] = 0x02 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 56:
				pos[0] = 0x03 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 64:
				pos[0] = 0x04 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 80:
				pos[0] = 0x05 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 96:
				pos[0] = 0x06 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 112:
				pos[0] = 0x07 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 128:
				pos[0] = 0x08 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 160:
				pos[0] = 0x09 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 192:
				pos[0] = 0x0A << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 224:
				pos[0] = 0x0B << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 256:
				pos[0] = 0x0C << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 320:
				pos[0] = 0x0D << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 384:
				pos[0] = 0x0E << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 448:
				pos[0] = 0x0F << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 512:
				pos[0] = 0x10 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 576:
				pos[0] = 0x11 << 2;
				pos = pos[1:]
				*len += 1
				break;
			case 640:
				pos[0] = 0x12 << 2;
				pos = pos[1:]
				*len += 1
				break;
			default:
				pos[0] = 0x32 << 2;
				pos = pos[1:]
				*len += 1
				break; /* 640 Kb/s upper limit */
			}

			/* 3 bits bsmod, 4 bits num_channels, 1 bit full_svc */
			switch (stream.audioChannels) {
			case 1:
				pos[0] = 0x01 << 1;
				pos = pos[1:]
				*len += 1
				break; /* 1/0 */
			case 2:
				pos[0] = 0x02 << 1;
				pos = pos[1:]
				*len += 1
				break; /* 2/0 */
			case 3:
				pos[0] = 0x0A << 1;
				pos = pos[1:]
				*len += 1
				break; /* <= 3 */
			case 4:
				pos[0] = 0x0B << 1;
				pos = pos[1:]
				*len += 1
				break; /* <= 4 */
			case 5:
				pos[0] = 0x0C << 1;
				pos = pos[1:]
				*len += 1
				break; /* <= 5 */
			case 6:
			default:
				pos[0] = 0x0D << 1;
				pos = pos[1:]
				*len += 1
				break; /* <= 6 */
			}

			pos[0] = 0x00;
			pos = pos[1:]
			*len += 1
			break;
		}
	case PSMUX_ST_PS_AUDIO_DTS:
		/* FIXME */
		break;
	case PSMUX_ST_PS_AUDIO_LPCM:
		/* FIXME */
		break;
	default:
		break;
	}
}

func (stream *PsMuxStream) psmux_stream_get_pts() int64 {
	if (stream == nil) {
		return -1
	}
	return stream.last_pts
}

//crc32
var crc_tab []uint32 = []uint32{
	0x00000000, 0x04c11db7, 0x09823b6e, 0x0d4326d9, 0x130476dc, 0x17c56b6b,
	0x1a864db2, 0x1e475005, 0x2608edb8, 0x22c9f00f, 0x2f8ad6d6, 0x2b4bcb61,
	0x350c9b64, 0x31cd86d3, 0x3c8ea00a, 0x384fbdbd, 0x4c11db70, 0x48d0c6c7,
	0x4593e01e, 0x4152fda9, 0x5f15adac, 0x5bd4b01b, 0x569796c2, 0x52568b75,
	0x6a1936c8, 0x6ed82b7f, 0x639b0da6, 0x675a1011, 0x791d4014, 0x7ddc5da3,
	0x709f7b7a, 0x745e66cd, 0x9823b6e0, 0x9ce2ab57, 0x91a18d8e, 0x95609039,
	0x8b27c03c, 0x8fe6dd8b, 0x82a5fb52, 0x8664e6e5, 0xbe2b5b58, 0xbaea46ef,
	0xb7a96036, 0xb3687d81, 0xad2f2d84, 0xa9ee3033, 0xa4ad16ea, 0xa06c0b5d,
	0xd4326d90, 0xd0f37027, 0xddb056fe, 0xd9714b49, 0xc7361b4c, 0xc3f706fb,
	0xceb42022, 0xca753d95, 0xf23a8028, 0xf6fb9d9f, 0xfbb8bb46, 0xff79a6f1,
	0xe13ef6f4, 0xe5ffeb43, 0xe8bccd9a, 0xec7dd02d, 0x34867077, 0x30476dc0,
	0x3d044b19, 0x39c556ae, 0x278206ab, 0x23431b1c, 0x2e003dc5, 0x2ac12072,
	0x128e9dcf, 0x164f8078, 0x1b0ca6a1, 0x1fcdbb16, 0x018aeb13, 0x054bf6a4,
	0x0808d07d, 0x0cc9cdca, 0x7897ab07, 0x7c56b6b0, 0x71159069, 0x75d48dde,
	0x6b93dddb, 0x6f52c06c, 0x6211e6b5, 0x66d0fb02, 0x5e9f46bf, 0x5a5e5b08,
	0x571d7dd1, 0x53dc6066, 0x4d9b3063, 0x495a2dd4, 0x44190b0d, 0x40d816ba,
	0xaca5c697, 0xa864db20, 0xa527fdf9, 0xa1e6e04e, 0xbfa1b04b, 0xbb60adfc,
	0xb6238b25, 0xb2e29692, 0x8aad2b2f, 0x8e6c3698, 0x832f1041, 0x87ee0df6,
	0x99a95df3, 0x9d684044, 0x902b669d, 0x94ea7b2a, 0xe0b41de7, 0xe4750050,
	0xe9362689, 0xedf73b3e, 0xf3b06b3b, 0xf771768c, 0xfa325055, 0xfef34de2,
	0xc6bcf05f, 0xc27dede8, 0xcf3ecb31, 0xcbffd686, 0xd5b88683, 0xd1799b34,
	0xdc3abded, 0xd8fba05a, 0x690ce0ee, 0x6dcdfd59, 0x608edb80, 0x644fc637,
	0x7a089632, 0x7ec98b85, 0x738aad5c, 0x774bb0eb, 0x4f040d56, 0x4bc510e1,
	0x46863638, 0x42472b8f, 0x5c007b8a, 0x58c1663d, 0x558240e4, 0x51435d53,
	0x251d3b9e, 0x21dc2629, 0x2c9f00f0, 0x285e1d47, 0x36194d42, 0x32d850f5,
	0x3f9b762c, 0x3b5a6b9b, 0x0315d626, 0x07d4cb91, 0x0a97ed48, 0x0e56f0ff,
	0x1011a0fa, 0x14d0bd4d, 0x19939b94, 0x1d528623, 0xf12f560e, 0xf5ee4bb9,
	0xf8ad6d60, 0xfc6c70d7, 0xe22b20d2, 0xe6ea3d65, 0xeba91bbc, 0xef68060b,
	0xd727bbb6, 0xd3e6a601, 0xdea580d8, 0xda649d6f, 0xc423cd6a, 0xc0e2d0dd,
	0xcda1f604, 0xc960ebb3, 0xbd3e8d7e, 0xb9ff90c9, 0xb4bcb610, 0xb07daba7,
	0xae3afba2, 0xaafbe615, 0xa7b8c0cc, 0xa379dd7b, 0x9b3660c6, 0x9ff77d71,
	0x92b45ba8, 0x9675461f, 0x8832161a, 0x8cf30bad, 0x81b02d74, 0x857130c3,
	0x5d8a9099, 0x594b8d2e, 0x5408abf7, 0x50c9b640, 0x4e8ee645, 0x4a4ffbf2,
	0x470cdd2b, 0x43cdc09c, 0x7b827d21, 0x7f436096, 0x7200464f, 0x76c15bf8,
	0x68860bfd, 0x6c47164a, 0x61043093, 0x65c52d24, 0x119b4be9, 0x155a565e,
	0x18197087, 0x1cd86d30, 0x029f3d35, 0x065e2082, 0x0b1d065b, 0x0fdc1bec,
	0x3793a651, 0x3352bbe6, 0x3e119d3f, 0x3ad08088, 0x2497d08d, 0x2056cd3a,
	0x2d15ebe3, 0x29d4f654, 0xc5a92679, 0xc1683bce, 0xcc2b1d17, 0xc8ea00a0,
	0xd6ad50a5, 0xd26c4d12, 0xdf2f6bcb, 0xdbee767c, 0xe3a1cbc1, 0xe760d676,
	0xea23f0af, 0xeee2ed18, 0xf0a5bd1d, 0xf464a0aa, 0xf9278673, 0xfde69bc4,
	0x89b8fd09, 0x8d79e0be, 0x803ac667, 0x84fbdbd0, 0x9abc8bd5, 0x9e7d9662,
	0x933eb0bb, 0x97ffad0c, 0xafb010b1, 0xab710d06, 0xa6322bdf, 0xa2f33668,
	0xbcb4666d, 0xb8757bda, 0xb5365d03, 0xb1f740b4,
}

func calc_crc32(data []byte) uint32 {
	var crc uint32 = 0xffffffff
	for _, v := range data {
		crc = (crc << 8) ^ crc_tab[((crc>>24)^uint32(v))&0xff]
	}
	return crc
}
