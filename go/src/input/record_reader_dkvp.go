package input

import (
	"io"
	"strconv"
	"strings"

	"mlr/src/cli"
	"mlr/src/lib"
	"mlr/src/types"
)

type RecordReaderDKVP struct {
	readerOptions *cli.TReaderOptions
}

func NewRecordReaderDKVP(readerOptions *cli.TReaderOptions) *RecordReaderDKVP {
	return &RecordReaderDKVP{
		readerOptions: readerOptions,
	}
}

func (reader *RecordReaderDKVP) Read(
	filenames []string,
	context types.Context,
	inputChannel chan<- *types.RecordAndContext,
	errorChannel chan error,
	downstreamDoneChannel <-chan bool, // for mlr head
) {
	if filenames != nil { // nil for mlr -n
		if len(filenames) == 0 { // read from stdin
			handle, err := lib.OpenStdin(
				reader.readerOptions.Prepipe,
				reader.readerOptions.PrepipeIsRaw,
				reader.readerOptions.FileInputEncoding,
			)
			if err != nil {
				errorChannel <- err
			}
			reader.processHandle(handle, "(stdin)", &context, inputChannel, errorChannel, downstreamDoneChannel)
		} else {
			for _, filename := range filenames {
				handle, err := lib.OpenFileForRead(
					filename,
					reader.readerOptions.Prepipe,
					reader.readerOptions.PrepipeIsRaw,
					reader.readerOptions.FileInputEncoding,
				)
				if err != nil {
					errorChannel <- err
				} else {
					reader.processHandle(handle, filename, &context, inputChannel, errorChannel, downstreamDoneChannel)
					handle.Close()
				}
			}
		}
	}
	inputChannel <- types.NewEndOfStreamMarker(&context)
}

func (reader *RecordReaderDKVP) processHandle(
	handle io.Reader,
	filename string,
	context *types.Context,
	inputChannel chan<- *types.RecordAndContext,
	errorChannel chan error,
	downstreamDoneChannel <-chan bool, // for mlr head
) {
	context.UpdateForStartOfFile(filename)

	scanner := NewLineScanner(handle, reader.readerOptions.IRS)
	for scanner.Scan() {

		// See if downstream processors will be ignoring further data (e.g. mlr
		// head).  If so, stop reading. This makes 'mlr head hugefile' exit
		// quickly, as it should.
		eof := false
		select {
		case _ = <-downstreamDoneChannel:
			eof = true
			break
		default:
			break
		}
		if eof {
			break
		}

		// xxx what of lib.IsEOF
		// xxx what of errchan
		line := scanner.Text()

		// Check for comments-in-data feature
		if strings.HasPrefix(line, reader.readerOptions.CommentString) {
			if reader.readerOptions.CommentHandling == cli.PassComments {
				inputChannel <- types.NewOutputString(line+"\n", context)
				continue
			} else if reader.readerOptions.CommentHandling == cli.SkipComments {
				continue
			}
			// else comments are data
		}

		// This is how to do a chomp:
		//line = strings.TrimRight(line, "\n")

		// xxx temp pending autodetect, and pending more windows-port work
		//line = strings.TrimRight(line, "\r")

		record := reader.recordFromDKVPLine(line)
		context.UpdateForInputRecord()
		inputChannel <- types.NewRecordAndContext(
			record,
			context,
		)
	}
}

// ----------------------------------------------------------------
func (reader *RecordReaderDKVP) recordFromDKVPLine(
	line string,
) *types.Mlrmap {
	record := types.NewMlrmap()
	pairs := lib.RegexSplitString(reader.readerOptions.IFSRegex, line, -1)

	for i, pair := range pairs {
		kv := lib.RegexSplitString(reader.readerOptions.IPSRegex, pair, 2)
		// TODO check length 0. also, check input is empty since "".split() -> [""] not []
		if len(kv) == 0 {
			// Ignore. This is expected when splitting with repeated IFS.
		} else if len(kv) == 1 {
			// E.g the pair has no equals sign: "a" rather than "a=1" or
			// "a=".  Here we use the positional index as the key. This way
			// DKVP is a generalization of NIDX.
			key := strconv.Itoa(i + 1) // Miller userspace indices are 1-up
			value := types.MlrvalPointerFromInferredTypeForDataFiles(kv[0])
			record.PutReference(key, value)
		} else {
			key := kv[0]
			value := types.MlrvalPointerFromInferredTypeForDataFiles(kv[1])
			record.PutReference(key, value)
		}
	}
	return record
}
