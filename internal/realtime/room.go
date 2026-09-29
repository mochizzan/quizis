package realtime

import "strconv"

// TopicForQuizID is the student topic for a quiz (spec §6.3).
func TopicForQuizID(id uint64) string {
	return TopicPrefix + strconv.FormatUint(id, 10)
}

// TeacherTopicForQuizID is the monitor topic for a quiz.
func TeacherTopicForQuizID(id uint64) string {
	return TopicForQuizID(id) + ":teacher"
}
